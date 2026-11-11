// Package subs holds the rules of recurring purchases ("suscríbete y ahorra").
package subs

import (
	"errors"
	"time"
)

// Plans are the delivery frequencies a store can offer, shortest first.
var Plans = []string{"1w", "2w", "1m", "2m", "3m", "6m"}

var labels = map[string]string{"1w": "cada semana", "2w": "cada 2 semanas", "1m": "cada mes", "2m": "cada 2 meses", "3m": "cada 3 meses", "6m": "cada 6 meses"}

// Label says a plan in words ("cada mes"); "" for unknown plans.
func Label(plan string) string { return labels[plan] }

// Next returns the date of the delivery after t.
func Next(t time.Time, plan string) time.Time {
	switch plan {
	case "1w":
		return t.AddDate(0, 0, 7)
	case "2w":
		return t.AddDate(0, 0, 14)
	case "2m":
		return t.AddDate(0, 2, 0)
	case "3m":
		return t.AddDate(0, 3, 0)
	case "6m":
		return t.AddDate(0, 6, 0)
	}
	return t.AddDate(0, 1, 0)
}

// Config is how a store offers subscriptions.
type Config struct {
	Enabled     bool     `json:"enabled"`
	DiscountBps int64    `json:"discountBps"` // discount on every delivery (1000 = 10%)
	Plans       []string `json:"plans"`
	ProductIDs  []string `json:"productIds"` // products that can be bought on subscription
}

// Normalize validates the configuration.
func (c *Config) Normalize() error {
	seen := map[string]bool{}
	out := []string{}
	for _, p := range Plans { // keeps the canonical order
		for _, x := range c.Plans {
			if x == p && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	for _, x := range c.Plans {
		if labels[x] == "" {
			return errors.New("Frecuencia desconocida: " + x)
		}
	}
	c.Plans = out
	ids := []string{}
	seen = map[string]bool{}
	for _, id := range c.ProductIDs {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	c.ProductIDs = ids
	switch {
	case c.DiscountBps < 0 || c.DiscountBps > 9000:
		return errors.New("El descuento debe estar entre 0% y 90%")
	case len(ids) > 2000:
		return errors.New("Máximo 2000 productos con suscripción")
	case c.Enabled && len(out) == 0:
		return errors.New("Elige al menos una frecuencia de entrega")
	}
	return nil
}

// Allows reports whether the product can be bought with the plan.
func (c *Config) Allows(productID, plan string) bool {
	if c == nil || !c.Enabled || plan == "" {
		return false
	}
	ok := false
	for _, p := range c.Plans {
		ok = ok || p == plan
	}
	if !ok {
		return false
	}
	for _, id := range c.ProductIDs {
		if id == productID {
			return true
		}
	}
	return false
}
