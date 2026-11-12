package api

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/media"
)

// routesCatalog registers the advanced catalog and media library.
func (s *Server) routesCatalog(handle func(string, http.HandlerFunc)) {
	if s.Media == nil {
		// Tests and ad-hoc runs: keep files under the temp dir. cmd/apexx sets
		// a durable directory (APEXX_MEDIA_DIR).
		if l, err := media.NewLocal(filepath.Join(os.TempDir(), "apexx-media")); err == nil {
			s.Media = l
		}
	}
	s.uploadSlots = make(chan struct{}, 2)

	// Products.
	handle("GET /v1/products", s.authed("catalog", auth.Read, s.listProducts))
	handle("POST /v1/products", s.authed("catalog", auth.Edit, s.createProduct))
	handle("POST /v1/products/bulk", s.authed("catalog", auth.Edit, s.bulkProducts))
	handle("GET /v1/products/{id}", s.authed("catalog", auth.Read, s.getProduct))
	handle("GET /v1/products/{id}/quantity-rules", s.staff("catalog", auth.Read, s.getQtyRules))
	handle("PUT /v1/products/{id}/quantity-rules", s.authed("catalog", auth.Edit, s.putQtyRules))
	handle("GET /v1/products/{id}/customizations", s.staff("catalog", auth.Read, s.getCustoms))
	handle("PUT /v1/products/{id}/customizations", s.authed("catalog", auth.Edit, s.putCustoms))
	handle("GET /v1/products/{id}/translations", s.staff("catalog", auth.Read, s.getProductTexts))
	handle("PUT /v1/products/{id}/translations", s.authed("catalog", auth.Edit, s.putProductTexts))
	handle("PUT /v1/products/{id}", s.authed("catalog", auth.Edit, s.putProduct))
	handle("DELETE /v1/products/{id}", s.authed("catalog", auth.Admin, s.deleteProduct))
	handle("POST /v1/catalog/variants/generate", s.staff("catalog", auth.Read, s.generateVariants))
	handle("GET /v1/catalog/summary", s.authed("catalog", auth.Read, s.catalogSummary))

	// Organization.
	handle("GET /v1/catalog/categories", s.authed("catalog", auth.Read, s.listCategories))
	handle("POST /v1/catalog/categories", s.authed("catalog", auth.Edit, s.createCategory))
	handle("POST /v1/catalog/categories/reorder", s.authed("catalog", auth.Edit, s.reorderCategories))
	handle("PUT /v1/catalog/categories/{id}", s.authed("catalog", auth.Edit, s.updateCategory))
	handle("DELETE /v1/catalog/categories/{id}", s.authed("catalog", auth.Edit, s.deleteCategory))
	handle("GET /v1/catalog/brands", s.authed("catalog", auth.Read, s.listBrands))
	handle("POST /v1/catalog/brands", s.authed("catalog", auth.Edit, s.createBrand))
	handle("PUT /v1/catalog/brands/{id}", s.authed("catalog", auth.Edit, s.updateBrand))
	handle("DELETE /v1/catalog/brands/{id}", s.authed("catalog", auth.Edit, s.deleteBrand))
	handle("GET /v1/catalog/collections", s.authed("catalog", auth.Read, s.listCollections))
	handle("POST /v1/catalog/collections", s.authed("catalog", auth.Edit, s.createCollection))
	handle("POST /v1/catalog/collections/preview", s.staff("catalog", auth.Read, s.previewCollection))
	handle("GET /v1/catalog/collections/{id}", s.authed("catalog", auth.Read, s.getCollection))
	handle("PUT /v1/catalog/collections/{id}", s.authed("catalog", auth.Edit, s.updateCollection))
	handle("DELETE /v1/catalog/collections/{id}", s.authed("catalog", auth.Edit, s.deleteCollection))
	handle("GET /v1/catalog/attributes", s.authed("catalog", auth.Read, s.listAttributes))
	handle("POST /v1/catalog/attributes", s.authed("catalog", auth.Edit, s.createAttribute))
	handle("PUT /v1/catalog/attributes/{id}", s.authed("catalog", auth.Edit, s.updateAttribute))
	handle("DELETE /v1/catalog/attributes/{id}", s.authed("catalog", auth.Edit, s.deleteAttribute))
	handle("GET /v1/catalog/terms/{kind}", s.authed("catalog", auth.Read, s.listTerms))
	handle("POST /v1/catalog/terms/{kind}", s.authed("catalog", auth.Edit, s.addTerm))
	handle("DELETE /v1/catalog/terms/{kind}", s.authed("catalog", auth.Edit, s.deleteTerm))

	// Media library. Files are public and immutable; the index is private.
	handle("GET /v1/catalog/media", s.authed("catalog", auth.Read, s.listMedia))
	handle("POST /v1/catalog/media", s.authed("catalog", auth.Edit, s.uploadMedia))
	handle("GET /v1/catalog/media/{id}", s.authed("catalog", auth.Read, s.getMedia))
	handle("PATCH /v1/catalog/media/{id}", s.authed("catalog", auth.Edit, s.patchMedia))
	handle("DELETE /v1/catalog/media/{id}", s.authed("catalog", auth.Edit, s.deleteMedia))
	handle("GET /media/{tenant}/{id}/{file}", s.serveMedia)

	// CSV import and export.
	handle("GET /v1/catalog/imports", s.authed("catalog", auth.Read, s.listImports))
	handle("POST /v1/catalog/imports", s.authed("catalog", auth.Edit, s.createImport))
	handle("GET /v1/catalog/imports/{id}", s.authed("catalog", auth.Read, s.getImport))
	handle("POST /v1/catalog/imports/{id}/apply", s.authed("catalog", auth.Edit, s.applyImport))
	handle("GET /v1/catalog/export.csv", s.authed("catalog", auth.Read, s.exportCSV))
	handle("GET /v1/catalog/import-template.csv", s.authed("catalog", auth.Read, s.importTemplate))

	// Storefront: category and collection pages (the catalog page itself is /c).
	handle("GET /s/{tenant}/{site}/c/{handle}", s.shopCategory)
	handle("GET /s/{tenant}/{site}/col/{handle}", s.shopCollection)
}
