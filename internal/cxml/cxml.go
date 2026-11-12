// Package cxml reads and writes the parts of cXML 1.2 that punchout uses:
// PunchOutSetupRequest/Response, PunchOutOrderMessage and OrderRequest.
package cxml

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Credential is a party of a cXML header.
type Credential struct {
	Domain       string `xml:"domain,attr"`
	Identity     string `xml:"Identity"`
	SharedSecret string `xml:"SharedSecret,omitempty"`
}

// Header identifies who sends a document.
type Header struct {
	From   []Credential `xml:"From>Credential"`
	To     []Credential `xml:"To>Credential"`
	Sender struct {
		Credential []Credential `xml:"Credential"`
		UserAgent  string       `xml:"UserAgent"`
	} `xml:"Sender"`
}

// Money is an amount with its currency.
type Money struct {
	Currency string `xml:"currency,attr"`
	Value    string `xml:",chardata"`
}

// Item is a line in a cart or purchase order.
type Item struct {
	Quantity     string `xml:"quantity,attr"`
	LineNumber   string `xml:"lineNumber,attr,omitempty"`
	SupplierPart string `xml:"ItemID>SupplierPartID"`
	UnitPrice    Money  `xml:"ItemDetail>UnitPrice>Money"`
	Description  string `xml:"ItemDetail>Description"`
	UnitOfMeas   string `xml:"ItemDetail>UnitOfMeasure"`
}

// Document is an incoming cXML document.
type Document struct {
	XMLName   xml.Name `xml:"cXML"`
	PayloadID string   `xml:"payloadID,attr"`
	Header    Header   `xml:"Header"`
	Request   struct {
		Setup *struct {
			Operation    string `xml:"operation,attr"`
			BuyerCookie  string `xml:"BuyerCookie"`
			BrowserPost  string `xml:"BrowserFormPost>URL"`
			Contact      string `xml:"Contact>Email"`
			SelectedItem string `xml:"SelectedItem>ItemID>SupplierPartID"`
		} `xml:"PunchOutSetupRequest"`
		Order *struct {
			Header struct {
				OrderID string `xml:"orderID,attr"`
				Total   Money  `xml:"Total>Money"`
				ShipTo  string `xml:"ShipTo>Address>Name"`
				Street  string `xml:"ShipTo>Address>PostalAddress>Street"`
				City    string `xml:"ShipTo>Address>PostalAddress>City"`
				Postal  string `xml:"ShipTo>Address>PostalAddress>PostalCode"`
				Email   string `xml:"Contact>Email"`
			} `xml:"OrderRequestHeader"`
			Items []Item `xml:"ItemOut"`
		} `xml:"OrderRequest"`
	} `xml:"Request"`
}

// Parse reads a cXML document (at most 1 MB, no external entities).
func Parse(r io.Reader) (*Document, error) {
	b, err := io.ReadAll(io.LimitReader(r, 1<<20+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 1<<20 {
		return nil, errors.New("cxml: document too large")
	}
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.Strict = true
	dec.Entity = map[string]string{} // no custom entities
	var d Document
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("cxml: %w", err)
	}
	return &d, nil
}

// SenderSecret is the shared secret the sender presented.
func (d *Document) SenderSecret() string {
	for _, c := range d.Header.Sender.Credential {
		if c.SharedSecret != "" {
			return c.SharedSecret
		}
	}
	return ""
}

// FromIdentity is the buyer organisation's identity.
func (d *Document) FromIdentity() string {
	if len(d.Header.From) > 0 {
		return strings.TrimSpace(d.Header.From[0].Identity)
	}
	return ""
}

func payloadID(host string) string {
	return strconv.FormatInt(time.Now().UnixNano(), 10) + "@" + host
}

func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

const prolog = `<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<!DOCTYPE cXML SYSTEM "http://xml.cxml.org/schemas/cXML/1.2.014/cXML.dtd">` + "\n"

// Response answers a request with a status (200 OK, 401 unauthorized...).
func Response(host string, code int, text string) []byte {
	return []byte(prolog + fmt.Sprintf(`<cXML payloadID="%s" timestamp="%s"><Response><Status code="%d" text="%s"/></Response></cXML>`,
		esc(payloadID(host)), time.Now().UTC().Format(time.RFC3339), code, esc(text)))
}

// SetupResponse gives the page where the buyer starts shopping.
func SetupResponse(host, startURL string) []byte {
	return []byte(prolog + fmt.Sprintf(`<cXML payloadID="%s" timestamp="%s"><Response><Status code="200" text="OK"/><PunchOutSetupResponse><StartPage><URL>%s</URL></StartPage></PunchOutSetupResponse></Response></cXML>`,
		esc(payloadID(host)), time.Now().UTC().Format(time.RFC3339), esc(startURL)))
}

// CartLine is one line sent back to the purchasing system.
type CartLine struct {
	SKU, Description string
	Qty, Unit        int64 // unit in minor units
}

func decimal(minor int64) string { return fmt.Sprintf("%d.%02d", minor/100, minor%100) }

// OrderMessage is the cart sent back to the purchasing system.
func OrderMessage(host, buyerCookie, currency string, lines []CartLine, from, to string) []byte {
	var total int64
	var items strings.Builder
	for _, l := range lines {
		total += l.Qty * l.Unit
		fmt.Fprintf(&items, `<ItemIn quantity="%d"><ItemID><SupplierPartID>%s</SupplierPartID></ItemID><ItemDetail><UnitPrice><Money currency="%s">%s</Money></UnitPrice><Description xml:lang="es">%s</Description><UnitOfMeasure>EA</UnitOfMeasure><Classification domain="UNSPSC">0</Classification></ItemDetail></ItemIn>`,
			l.Qty, esc(l.SKU), esc(currency), decimal(l.Unit), esc(l.Description))
	}
	return []byte(prolog + fmt.Sprintf(`<cXML payloadID="%s" timestamp="%s"><Header><From><Credential domain="NetworkID"><Identity>%s</Identity></Credential></From><To><Credential domain="NetworkID"><Identity>%s</Identity></Credential></To><Sender><Credential domain="NetworkID"><Identity>%s</Identity></Credential><UserAgent>Apexx</UserAgent></Sender></Header><Message><PunchOutOrderMessage><BuyerCookie>%s</BuyerCookie><PunchOutOrderMessageHeader operationAllowed="edit"><Total><Money currency="%s">%s</Money></Total></PunchOutOrderMessageHeader>%s</PunchOutOrderMessage></Message></cXML>`,
		esc(payloadID(host)), time.Now().UTC().Format(time.RFC3339), esc(from), esc(to), esc(from), esc(buyerCookie), esc(currency), decimal(total), items.String()))
}

// Minor reads a cXML decimal amount as minor units.
func Minor(v string) (int64, bool) {
	v = strings.TrimSpace(v)
	whole, frac, _ := strings.Cut(v, ".")
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || w < 0 || len(frac) > 4 {
		return 0, false
	}
	frac = (frac + "00")[:2]
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, false
	}
	return w*100 + f, true
}
