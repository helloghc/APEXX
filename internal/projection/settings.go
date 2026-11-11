package projection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync/atomic"

	"github.com/helloghc/apexx/internal/logistics"
	"github.com/helloghc/apexx/internal/sitecfg"
	"github.com/helloghc/apexx/internal/store"
)

// SiteView is what the storefront needs to know about a store besides the
// catalog: identity, checkout rules, policies, menus, redirects and the
// canonical domain. It is immutable; the projector swaps it on every change.
type SiteView struct {
	Settings  sitecfg.Settings
	Policies  map[string]sitecfg.Policy   // by slug
	Menus     map[string]sitecfg.Menu     // by id (header, footer, …)
	Redirects map[string]sitecfg.Redirect // active ones, by origin path
	Primary   string                      // canonical verified hostname, "" if none
	Version   string                      // changes whenever anything above changes
}

var emptySite = &SiteView{Policies: map[string]sitecfg.Policy{}, Menus: map[string]sitecfg.Menu{}, Redirects: map[string]sitecfg.Redirect{}}

// siteState is embedded in Tenant.
type siteState struct {
	site atomic.Pointer[SiteView]
}

// Site returns the storefront configuration of the tenant.
func (t *Tenant) Site() *SiteView {
	if v := t.site.Load(); v != nil {
		return v
	}
	return emptySite
}

// Logistics returns the shipping and tax rules of the tenant.
func (t *Tenant) Logistics() *logistics.Config {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Cart.Logistics
}

// applySettings reloads the part of the configuration named by key
// ("" = everything, used when the tenant is first loaded).
func (p *Projector) applySettings(ctx context.Context, t *Tenant, key string) error {
	switch key {
	case "", store.KeyShipping, store.KeyLocations, store.KeyTax, store.KeyProducts, store.KeyStore:
		cfg, err := p.db.LoadLogistics(ctx, t.ID)
		if err != nil {
			return err
		}
		t.mu.Lock()
		t.Cart.Logistics = cfg
		t.mu.Unlock()
	}
	switch key {
	case "", store.KeyStore, store.KeyPolicies, store.KeyMenus, store.KeyRedirects, store.KeyDomains:
		if err := p.applySite(ctx, t, key); err != nil {
			return err
		}
	}
	if key == store.KeyDomains {
		return p.loadHosts(ctx)
	}
	return nil
}

func (p *Projector) applySite(ctx context.Context, t *Tenant, key string) error {
	old := t.Site()
	v := &SiteView{Settings: old.Settings, Policies: old.Policies, Menus: old.Menus, Redirects: old.Redirects, Primary: old.Primary}
	all := key == ""
	if all || key == store.KeyStore {
		s, _, err := p.db.Settings(ctx, t.ID)
		if err != nil {
			return err
		}
		v.Settings = s
	}
	if all || key == store.KeyPolicies {
		ps, err := p.db.Policies(ctx, t.ID)
		if err != nil {
			return err
		}
		v.Policies = make(map[string]sitecfg.Policy, len(ps))
		for _, x := range ps {
			v.Policies[x.Slug] = x
		}
	}
	if all || key == store.KeyMenus {
		ms, err := p.db.Menus(ctx, t.ID)
		if err != nil {
			return err
		}
		v.Menus = make(map[string]sitecfg.Menu, len(ms))
		for _, x := range ms {
			v.Menus[x.ID] = x
		}
	}
	if all || key == store.KeyRedirects {
		rs, _, err := p.db.Redirects(ctx, t.ID, "", "", 0)
		if err != nil {
			return err
		}
		v.Redirects = make(map[string]sitecfg.Redirect, len(rs))
		for _, x := range rs {
			if x.Active {
				v.Redirects[x.From] = x
			}
		}
	}
	if all || key == store.KeyDomains {
		ds, err := p.db.Domains(ctx, t.ID)
		if err != nil {
			return err
		}
		v.Primary = ""
		for _, d := range ds {
			if d.Status == "verified" && (d.Primary || v.Primary == "") {
				v.Primary = d.Hostname
			}
		}
	}
	b, _ := json.Marshal([]any{v.Settings, v.Policies, v.Menus, v.Primary})
	sum := sha256.Sum256(b)
	v.Version = hex.EncodeToString(sum[:6])
	t.site.Store(v)
	return nil
}

// hosts maps verified hostnames to their store, for every tenant.
type hostIndex struct {
	hosts atomic.Pointer[map[string]store.HostTarget]
}

func (p *Projector) loadHosts(ctx context.Context) error {
	m, err := p.db.VerifiedHosts(ctx)
	if err != nil {
		return err
	}
	p.hostIdx.hosts.Store(&m)
	return nil
}

// Host returns the store a verified custom hostname belongs to.
func (p *Projector) Host(hostname string) (store.HostTarget, bool) {
	m := p.hostIdx.hosts.Load()
	if m == nil {
		return store.HostTarget{}, false
	}
	t, ok := (*m)[hostname]
	return t, ok
}
