package unisub

import (
	"ai-unisub/internal/aiprovider"
	"ai-unisub/internal/common"
	"ai-unisub/internal/service"
	"encoding/json"
	"net/http"
)

func (m *APIModule) aiCatalog(ctx service.ModuleContext, w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) < 1 || len(parts) > 2 {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		if len(parts) == 2 {
			for _, supplier := range ctx.AIProviders().Catalog().Suppliers {
				if supplier.ID == parts[1] {
					writeJSON(w, 200, supplier)
					return
				}
			}
			http.NotFound(w, r)
			return
		}
		writeJSON(w, 200, map[string]any{"catalog": ctx.AIProviders().Catalog(), "builtin_suppliers": aiprovider.BuiltinSuppliers()})
		return
	}
	if !isAdmin(r) {
		common.WriteError(w, 403, common.MessageForbidden)
		return
	}
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, PUT")
		w.WriteHeader(405)
		return
	}
	var c aiprovider.Catalog
	var supplier aiprovider.Supplier
	if len(parts) == 2 {
		if !decodeJSON(w, r, &supplier) {
			return
		}
		if supplier.ID != parts[1] {
			common.WriteError(w, 400, "supplier ID must match the URL")
			return
		}
	} else if !decodeJSON(w, r, &c) {
		return
	}
	m.mutations.Lock()
	defer m.mutations.Unlock()
	if len(parts) == 2 {
		c = ctx.AIProviders().Catalog()
		found := false
		for i := range c.Suppliers {
			if c.Suppliers[i].ID == supplier.ID {
				c.Suppliers[i] = supplier
				found = true
				break
			}
		}
		if !found {
			http.NotFound(w, r)
			return
		}
	}
	if err := aiprovider.ValidateCatalog(c); err != nil {
		common.WriteError(w, 400, err.Error())
		return
	}
	raw, _ := json.Marshal(c)
	if err := ctx.Database().SaveModuleConfig(aiprovider.ModuleConfigKey, raw); err != nil {
		common.WriteError(w, 500, "could not save AI catalog")
		return
	}
	_ = ctx.AIProviders().SetCatalog(c)
	writeJSON(w, 200, map[string]any{"catalog": c, "builtin_suppliers": aiprovider.BuiltinSuppliers()})
}
