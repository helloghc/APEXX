package cart

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Gifts: the shopper may ask to wrap the order, add a card message and get a
// gift receipt (the packing slip without prices). The store decides whether
// it offers them and how much wrapping costs.

// GiftOptions is the store's setting.
type GiftOptions struct {
	Enabled    bool  `json:"enabled"`    // offer "this is a gift" at checkout
	WrapPrice  int64 `json:"wrapPrice"`  // in the store currency; 0 = free wrapping
	MessageMax int   `json:"messageMax"` // characters of the card message (default 250)
	Receipt    bool  `json:"receipt"`    // offer a gift receipt without prices
	Registries bool  `json:"registries"` // shoppers can open gift registries
}

// GiftError is a gift request the store cannot take (a message too long).
type GiftError string

func (e GiftError) Error() string { return string(e) }

// Gift is what the shopper asked for.
type Gift struct {
	Wrap       bool   `json:"wrap"`
	Message    string `json:"message,omitempty"`
	HidePrices bool   `json:"hidePrices,omitempty"`
}

// Clean validates the setting.
func (o GiftOptions) Clean() (GiftOptions, error) {
	if o.WrapPrice < 0 || o.WrapPrice > 10_000_00 {
		return o, fmt.Errorf("la envoltura cuesta de 0 a 10,000")
	}
	if o.MessageMax == 0 {
		o.MessageMax = 250
	}
	if o.MessageMax < 20 || o.MessageMax > 1000 {
		return o, fmt.Errorf("el mensaje admite de 20 a 1000 caracteres")
	}
	return o, nil
}

// Normalize applies the store's setting to the shopper's request: nil when
// there is no gift or the store does not offer gifts.
func (o GiftOptions) Normalize(g *Gift) (*Gift, error) {
	if g == nil || !o.Enabled {
		return nil, nil
	}
	max := o.MessageMax
	if max == 0 {
		max = 250
	}
	msg := strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' || r >= ' ' && r != 0x7f {
			return r
		}
		return -1
	}, g.Message))
	if utf8.RuneCountInString(msg) > max {
		return nil, GiftError(fmt.Sprintf("El mensaje de regalo admite hasta %d caracteres", max))
	}
	out := &Gift{Wrap: g.Wrap, Message: msg, HidePrices: g.HidePrices && o.Receipt}
	if !out.Wrap && out.Message == "" && !out.HidePrices {
		return nil, nil
	}
	return out, nil
}

// wrapFee is the charge for wrapping (nil when free or not asked).
func (o GiftOptions) wrapFee(g *Gift) *Fee {
	if g == nil || !g.Wrap || o.WrapPrice <= 0 {
		return nil
	}
	return &Fee{ID: "gift-wrap", Kind: "fee", Name: "Envoltura de regalo", Amount: o.WrapPrice, Reason: "Pediste envolver tu pedido para regalo"}
}
