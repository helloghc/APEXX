package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/builder"
)

// SitePersister stores a builder site of one tenant. It implements builder.Persister.
type SitePersister struct {
	DB     *DB
	Tenant string
	Site   string
	Actor  func() string
}

func (p SitePersister) ctx() context.Context { return context.Background() }

// Ensure creates the site row if needed.
func (p SitePersister) Ensure(name string) error {
	return p.DB.Tx(p.ctx(), p.Tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(p.ctx(), `INSERT INTO sites (tenant_id, id, name) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, p.Tenant, p.Site, name)
		return err
	})
}

// SaveDraft implements builder.Persister.
func (p SitePersister) SaveDraft(handle string, t builder.Template, version int64) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return p.DB.Tx(p.ctx(), p.Tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(p.ctx(), `INSERT INTO site_drafts (tenant_id, site_id, handle, template) VALUES ($1,$2,$3,$4)
			ON CONFLICT (tenant_id, site_id, handle) DO UPDATE SET template=EXCLUDED.template, updated_at=now()`, p.Tenant, p.Site, handle, b); err != nil {
			return err
		}
		// Optimistic concurrency across nodes: the stored version must be the
		// one this node based its edit on.
		ct, err := tx.Exec(p.ctx(), `UPDATE sites SET draft_version=$3 WHERE tenant_id=$1 AND id=$2 AND draft_version=$3-1`, p.Tenant, p.Site, version)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return builder.ErrStaleDraft
		}
		return Emit(p.ctx(), tx, p.Tenant, "site.draft", p.Site, map[string]any{"version": version})
	})
}

// SaveRevision implements builder.Persister.
func (p SitePersister) SaveRevision(r *builder.Revision) error {
	tpl, err := json.Marshal(r.Templates)
	if err != nil {
		return err
	}
	pages := map[string]string{}
	for h, b := range r.Pages {
		pages[h] = string(b)
	}
	pg, _ := json.Marshal(pages)
	return p.DB.Tx(p.ctx(), p.Tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(p.ctx(), `INSERT INTO site_revisions (tenant_id, site_id, id, parent_id, created_by, message, checksum, templates, pages)
			VALUES ($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9) ON CONFLICT DO NOTHING`,
			p.Tenant, p.Site, r.ID, r.ParentID, r.CreatedBy, r.Message, r.Checksum, tpl, pg)
		if err != nil {
			return err
		}
		return Audit(p.ctx(), tx, p.Tenant, r.CreatedBy, "site.revision", p.Site+"/"+r.ID, map[string]any{"message": r.Message})
	})
}

// SaveRelease implements builder.Persister.
func (p SitePersister) SaveRelease(rel builder.Release) error {
	return p.DB.Tx(p.ctx(), p.Tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(p.ctx(), `INSERT INTO site_releases (tenant_id, site_id, revision_id, published_at, published_by, duration_micros, rolled_back_from)
			VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''))`, p.Tenant, p.Site, rel.RevisionID, rel.PublishedAt, rel.PublishedBy, rel.DurationMicros, rel.RolledBackFrom); err != nil {
			return err
		}
		if _, err := tx.Exec(p.ctx(), `UPDATE sites SET live_revision=$3 WHERE tenant_id=$1 AND id=$2`, p.Tenant, p.Site, rel.RevisionID); err != nil {
			return err
		}
		action := "site.publish"
		if rel.RolledBackFrom != "" {
			action = "site.rollback"
		}
		if err := Emit(p.ctx(), tx, p.Tenant, "site.released", p.Site, rel); err != nil {
			return err
		}
		return Audit(p.ctx(), tx, p.Tenant, rel.PublishedBy, action, p.Site+"/"+rel.RevisionID, rel)
	})
}

// LoadSite reads every persisted piece of a site. found is false for a new site.
func (p SitePersister) LoadSite() (drafts map[string]builder.Template, version int64, revs []*builder.Revision, rels []builder.Release, found bool, err error) {
	ctx := p.ctx()
	drafts = map[string]builder.Template{}
	err = p.DB.Tx(ctx, p.Tenant, func(tx pgx.Tx) error {
		e := tx.QueryRow(ctx, `SELECT draft_version FROM sites WHERE tenant_id=$1 AND id=$2 AND archived_at IS NULL`, p.Tenant, p.Site).Scan(&version)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		found = true
		rows, e := tx.Query(ctx, `SELECT handle, template FROM site_drafts WHERE tenant_id=$1 AND site_id=$2`, p.Tenant, p.Site)
		if e != nil {
			return e
		}
		for rows.Next() {
			var h string
			var b []byte
			if e := rows.Scan(&h, &b); e != nil {
				rows.Close()
				return e
			}
			var t builder.Template
			if e := json.Unmarshal(b, &t); e != nil {
				rows.Close()
				return e
			}
			drafts[h] = t
		}
		rows.Close()
		rows, e = tx.Query(ctx, `SELECT id, COALESCE(parent_id,''), created_at, created_by, message, checksum, templates, pages FROM site_revisions WHERE tenant_id=$1 AND site_id=$2`, p.Tenant, p.Site)
		if e != nil {
			return e
		}
		for rows.Next() {
			r := &builder.Revision{Pages: map[string][]byte{}}
			var tpl, pg []byte
			if e := rows.Scan(&r.ID, &r.ParentID, &r.CreatedAt, &r.CreatedBy, &r.Message, &r.Checksum, &tpl, &pg); e != nil {
				rows.Close()
				return e
			}
			_ = json.Unmarshal(tpl, &r.Templates)
			var pages map[string]string
			_ = json.Unmarshal(pg, &pages)
			for h, html := range pages {
				r.Pages[h] = []byte(html)
			}
			revs = append(revs, r)
		}
		rows.Close()
		rows, e = tx.Query(ctx, `SELECT revision_id, published_at, published_by, duration_micros, COALESCE(rolled_back_from,'') FROM site_releases WHERE tenant_id=$1 AND site_id=$2 ORDER BY seq`, p.Tenant, p.Site)
		if e != nil {
			return e
		}
		rels, e = pgx.CollectRows(rows, func(r pgx.CollectableRow) (builder.Release, error) {
			var x builder.Release
			return x, r.Scan(&x.RevisionID, &x.PublishedAt, &x.PublishedBy, &x.DurationMicros, &x.RolledBackFrom)
		})
		return e
	})
	return
}
