package store

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/catalog"
)

// ApplyNewProducts writes the products of an import that do not exist yet
// with a few set statements (INSERT … SELECT FROM unnest) instead of a round
// of statements per product. Each product is built exactly as the
// one-by-one import builds it (applyImportFields, importVariants), gets the
// same rows, the same events and one audit entry, so the result is the same.
//
// Products whose handle already exists are not touched: their indexes come
// back in existing, for the one-by-one path, which merges field by field. A
// product that cannot be written (a SKU that belongs to another product, an
// invalid option set) fails alone with an ApplyError, like before. If a set
// statement itself fails, nothing of the call is written and err says why;
// the caller can then fall back to the one-by-one path.
func (d *DB) ApplyNewProducts(ctx context.Context, tenant, actor string, ips []catalog.ImportProduct, opt ImportOptions) (out []ImportOutcome, existing []int, err error) {
	out = make([]ImportOutcome, len(ips))
	err = d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		existing = existing[:0]
		handles := make([]string, len(ips))
		for i, ip := range ips {
			handles[i] = ip.Handle
		}
		rows, err := tx.Query(ctx, `SELECT handle FROM products WHERE tenant_id=$1 AND handle = ANY($2)`, tenant, handles)
		if err != nil {
			return err
		}
		have, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		exists := map[string]bool{}
		for _, h := range have {
			exists[h] = true
		}

		// Brands and categories, created once per distinct name or path.
		brandOf, catOf := map[string]string{}, map[string]string{}
		type built struct {
			i int
			p catalog.Product
			w ProductWrite
		}
		var todo []built
		for i, ip := range ips {
			if exists[ip.Handle] {
				existing = append(existing, i)
				continue
			}
			p := catalog.Product{ID: NewID("prod_"), Handle: ip.Handle, Status: "draft"}
			applyImportFields(&p, ip)
			if ip.Brand != "" {
				id, ok := brandOf[ip.Brand]
				if !ok {
					if id, err = ensureBrandTx(ctx, tx, tenant, actor, ip.Brand); err != nil {
						return err
					}
					brandOf[ip.Brand] = id
				}
				p.BrandID = id
			}
			if len(ip.Categories) > 0 {
				p.Categories = p.Categories[:0]
				for _, path := range ip.Categories {
					id, ok := catOf[path]
					if !ok {
						if id, err = ensureCategoryPathTx(ctx, tx, tenant, actor, path); err != nil {
							return err
						}
						catOf[path] = id
					}
					p.Categories = append(p.Categories, id)
				}
			}
			w, e := importVariants(&p, ip, opt, true, func(string) (int64, bool) { return 0, false }) // a new SKU has no price yet
			if e != nil {
				out[i] = ImportOutcome{Err: e}
				continue
			}
			if p.Status == "" {
				p.Status = "active"
			}
			w.Product = p
			todo = append(todo, built{i, p, w})
		}

		// A SKU that already belongs to another product, or to an earlier
		// product of this same call, fails its product (as the unique index
		// makes the one-by-one path fail).
		var skus []string
		for _, b := range todo {
			for _, v := range b.p.Variants {
				skus = append(skus, v.SKU)
			}
		}
		taken := map[string]bool{}
		if len(skus) > 0 {
			rows, err := tx.Query(ctx, `SELECT sku FROM variants WHERE tenant_id=$1 AND sku = ANY($2)`, tenant, skus)
			if err != nil {
				return err
			}
			ts, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				return err
			}
			for _, s := range ts {
				taken[s] = true
			}
		}
		ok := todo[:0]
		for _, b := range todo {
			clash := false
			for _, v := range b.p.Variants {
				clash = clash || taken[v.SKU]
			}
			if clash {
				out[b.i] = ImportOutcome{Err: &ApplyError{"Un SKU del producto ya pertenece a otro producto"}}
				continue
			}
			for _, v := range b.p.Variants {
				taken[v.SKU] = true
			}
			ok = append(ok, b)
		}
		todo = ok
		if len(todo) == 0 {
			return nil
		}

		// The brand name stored on the product is the brand's own.
		var bids []string
		for _, b := range todo {
			if b.p.BrandID != "" {
				bids = append(bids, b.p.BrandID)
			}
		}
		brandName := map[string]string{}
		if len(bids) > 0 {
			rows, err := tx.Query(ctx, `SELECT id, name FROM brands WHERE tenant_id=$1 AND id = ANY($2)`, tenant, bids)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id, name string
				if err := rows.Scan(&id, &name); err != nil {
					rows.Close()
					return err
				}
				brandName[id] = name
			}
			rows.Close()
		}

		n := len(todo)
		col := func() []string { return make([]string, n) }
		ids, hs, titles, descs, brands, cats, attrs, statuses := col(), col(), col(), col(), col(), col(), col(), col()
		bidsCol, types, tags, opts, seoT, seoD, vmedia, related := col(), col(), col(), col(), col(), col(), col(), col()
		for k, b := range todo {
			p := b.p
			p.Brand = brandName[p.BrandID]
			todo[k].p = p
			a, _ := json.Marshal(nonNilMap(p.Attributes))
			o, _ := json.Marshal(p.Options)
			if p.Options == nil {
				o = []byte("[]")
			}
			c, _ := json.Marshal(nonNil(p.Categories))
			t, _ := json.Marshal(nonNil(p.Tags))
			vm := map[string]string{}
			for _, v := range p.Variants {
				if v.MediaID != "" {
					vm[v.SKU] = v.MediaID
				}
			}
			vmb, _ := json.Marshal(vm)
			rel, _ := json.Marshal(p.Related)
			ids[k], hs[k], titles[k], descs[k], brands[k], cats[k], attrs[k], statuses[k] = p.ID, p.Handle, p.Title, p.Description, p.Brand, string(c), string(a), p.Status
			bidsCol[k], types[k], tags[k], opts[k], seoT[k], seoD[k], vmedia[k], related[k] = p.BrandID, p.Type, string(t), string(o), p.SEOTitle, p.SEODescription, string(vmb), string(rel)
		}
		vrows, err := tx.Query(ctx, `
			INSERT INTO products (tenant_id, id, handle, title, description, brand, categories, attributes, status,
				brand_id, product_type, tags, options, seo_title, seo_description, publish_at, media, variant_media, related)
			SELECT $1, id, handle, title, descr, brand,
				ARRAY(SELECT jsonb_array_elements_text(cats::jsonb)), attrs::jsonb, status,
				bid, typ, ARRAY(SELECT jsonb_array_elements_text(tags::jsonb)), opts::jsonb, seot, seod, NULL, '{}', vm::jsonb, rel::jsonb
			FROM unnest($2::text[], $3::text[], $4::text[], $5::text[], $6::text[], $7::text[], $8::text[], $9::text[],
				$10::text[], $11::text[], $12::text[], $13::text[], $14::text[], $15::text[], $16::text[], $17::text[])
				AS x(id, handle, title, descr, brand, cats, attrs, status, bid, typ, tags, opts, seot, seod, vm, rel)
			RETURNING id, version`,
			tenant, ids, hs, titles, descs, brands, cats, attrs, statuses, bidsCol, types, tags, opts, seoT, seoD, vmedia, related)
		if err != nil {
			return err
		}
		version := map[string]int64{}
		for vrows.Next() {
			var id string
			var v int64
			if err := vrows.Scan(&id, &v); err != nil {
				vrows.Close()
				return err
			}
			version[id] = v
		}
		vrows.Close()
		if err := vrows.Err(); err != nil {
			return err
		}
		if err := d.sandboxRoom(ctx, tx, tenant); err != nil {
			return err
		}

		var vSKU, vPID, vTitle, vOpts, vBar []string
		var vW, vPos []int64
		var pSKU, pCur []string
		var pAmt []int64
		var pCmp []*int64
		var sSKU, sLoc []string
		var sQty []int64
		var eTopic, eKey, ePay []string
		entries := make([]auditEntry, 0, n)
		for _, b := range todo {
			p, w := b.p, b.w
			for i, v := range p.Variants {
				o, _ := json.Marshal(nonNilMap(v.Options))
				vSKU, vPID, vTitle, vOpts, vBar = append(vSKU, v.SKU), append(vPID, p.ID), append(vTitle, v.Title), append(vOpts, string(o)), append(vBar, v.Barcode)
				vW, vPos = append(vW, v.WeightGrams), append(vPos, int64(i))
			}
			for sku, pr := range w.Prices {
				pSKU, pCur, pAmt, pCmp = append(pSKU, sku), append(pCur, pr.Currency), append(pAmt, pr.Amount), append(pCmp, pr.CompareAt)
				eTopic, eKey, ePay = append(eTopic, TopicPrice), append(eKey, w.Channel+"|"+sku), append(ePay, "null")
			}
			for sku, locs := range w.Stock {
				for loc, q := range locs {
					sSKU, sLoc, sQty = append(sSKU, sku), append(sLoc, loc), append(sQty, q)
				}
				eTopic, eKey, ePay = append(eTopic, TopicStock), append(eKey, sku), append(ePay, "null")
			}
			ver := version[p.ID]
			vp, _ := json.Marshal(map[string]any{"version": ver})
			eTopic, eKey, ePay = append(eTopic, TopicProduct), append(eKey, p.ID), append(ePay, string(vp))
			entries = append(entries, auditEntry{action: "product.save", target: p.ID, data: map[string]any{
				"version": ver, "title": p.Title, "status": p.Status, "variants": len(p.Variants), "prices": len(w.Prices), "stock": len(w.Stock)}})
		}
		bt := &pgx.Batch{}
		bt.Queue(`INSERT INTO variants (tenant_id, sku, product_id, title, options, barcode, weight_grams, position)
			SELECT $1, s, pid, t, o::jsonb, b, w, pos FROM unnest($2::text[], $3::text[], $4::text[], $5::text[], $6::text[], $7::bigint[], $8::bigint[]) AS x(s, pid, t, o, b, w, pos)`,
			tenant, vSKU, vPID, vTitle, vOpts, vBar, vW, vPos)
		if len(pSKU) > 0 {
			bt.Queue(`INSERT INTO prices (tenant_id, channel, sku, min_qty, amount, currency, compare_at)
				SELECT $1, $2, s, 1, a, c, cmp FROM unnest($3::text[], $4::bigint[], $5::text[], $6::bigint[]) AS x(s, a, c, cmp)
				ON CONFLICT (tenant_id, channel, sku, min_qty) DO UPDATE SET amount=EXCLUDED.amount, currency=EXCLUDED.currency, compare_at=EXCLUDED.compare_at`,
				tenant, opt.Channel, pSKU, pAmt, pCur, pCmp)
		}
		if len(sSKU) > 0 {
			bt.Queue(`INSERT INTO stock_levels (tenant_id, sku, location_id, on_hand)
				SELECT $1, s, l, q FROM unnest($2::text[], $3::text[], $4::bigint[]) AS x(s, l, q)
				ON CONFLICT (tenant_id, sku, location_id) DO UPDATE SET on_hand=EXCLUDED.on_hand, updated_at=now()`,
				tenant, sSKU, sLoc, sQty)
			if strings.HasPrefix(tenant, SandboxPrefix) {
				bt.Queue(`DELETE FROM sandbox_stock_moves WHERE tenant_id=$1 AND (sku, location_id) IN (SELECT s, l FROM unnest($2::text[], $3::text[]) AS x(s, l))`, tenant, sSKU, sLoc)
			}
		}
		bt.Queue(`INSERT INTO outbox (tenant_id, topic, key, payload) SELECT $1, t, k, p::jsonb FROM unnest($2::text[], $3::text[], $4::text[]) AS x(t, k, p)`,
			tenant, eTopic, eKey, ePay)
		if err := sendBatch(ctx, tx, bt, func(_ int, err error) error { return err }); err != nil {
			return err
		}
		if err := auditMany(ctx, tx, tenant, actor, entries); err != nil {
			return err
		}
		for _, b := range todo {
			out[b.i] = ImportOutcome{Created: true}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return out, existing, nil
}
