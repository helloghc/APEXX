package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/orders"
	"github.com/helloghc/apexx/internal/pdfdoc"
)

// Company invoices paid online from "Mi empresa", with a PDF receipt.

func (s *Server) routesCompanyInvoices(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/companies/{id}/invoices", s.authed("customers", auth.Read, noStorefront(s.companyInvoices)))
	handle("GET /v1/companies/{id}/receipts/{rid}/pdf", s.authed("customers", auth.Read, noStorefront(s.companyReceiptPDF)))
	handle("GET /s/{tenant}/api/account/company/invoices", s.shopCompanyInvoices)
	handle("POST /s/{tenant}/api/account/company/invoices/pay", s.shopPayInvoices)
	handle("GET /s/{tenant}/api/account/company/receipts/{rid}/pdf", s.shopReceiptPDF)
}

func (s *Server) invoicesAndReceipts(w http.ResponseWriter, r *http.Request, tenant, company string) {
	inv, err := s.Orders.CompanyInvoices(r.Context(), tenant, company)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	rec, err := s.Orders.Receipts(r.Context(), tenant, company)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	var open int64
	for _, x := range inv {
		open += x.Open
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": inv, "receipts": rec, "open": open})
}

func (s *Server) companyInvoices(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !s.companyExists(w, r, p.Tenant, r.PathValue("id")) {
		return
	}
	s.invoicesAndReceipts(w, r, p.Tenant, r.PathValue("id"))
}

func (s *Server) shopCompanyInvoices(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.portalCompany(w, r)
	if !ok {
		return
	}
	if !c.CanViewCredit {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "role_forbidden", Detail: "Tu rol en la empresa no permite ver sus facturas"})
		return
	}
	s.invoicesAndReceipts(w, r, r.PathValue("tenant"), c.ID)
}

func (s *Server) shopPayInvoices(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Origen no permitido"})
		return
	}
	c, email, ok := s.portalCompany(w, r)
	if !ok {
		return
	}
	if !c.CanViewCredit {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "role_forbidden", Detail: "Tu rol en la empresa no permite pagar sus facturas"})
		return
	}
	var in struct {
		Orders        []string `json:"orders"`
		PaymentMethod string   `json:"paymentMethod"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Orders) == 0 || len(in.Orders) > 200 || len(in.PaymentMethod) < 4 || in.PaymentMethod[:3] != "pm_" {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "orders", Code: "invalid", Message: "Elige las facturas y la tarjeta"})
		return
	}
	paidBy, actor := email, "company-buyer:"+c.ID
	if a := s.shopperActing(r); a != nil {
		paidBy, actor = email+" (por "+a.Name+")", "member:"+a.MemberID
	}
	rc, err := s.Orders.PayCompanyInvoices(r.Context(), r.PathValue("tenant"), actor, c.ID, paidBy, r.Header.Get("Idempotency-Key"), in.PaymentMethod, in.Orders)
	if errors.Is(err, orders.ErrNoOpenInvoices) {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "nothing_open", Detail: "Esas facturas ya están pagadas"})
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, rc)
}

func (s *Server) receiptPDF(tenant, companyName string, rc orders.Receipt) []byte {
	store := tenant
	if t, ok := s.Proj.Tenant(tenant); ok && t.Site().Settings.Name != "" {
		store = t.Site().Settings.Name
	}
	d := pdfdoc.New("Recibo " + rc.ID + " · " + store)
	d.Text(0, store, 11, true, 0)
	d.Line(11)
	d.Text(0, "Recibo de pago", 20, true, 0)
	d.Line(20)
	d.Text(0, "Folio: "+rc.ID+"   ·   Fecha: "+rc.At.Format("02/01/2006 15:04"), 9, false, 0)
	d.Line(9)
	d.Text(0, "Empresa: "+companyName+"   ·   Pagó: "+rc.PaidBy, 9, false, 0)
	d.Line(9)
	d.Text(0, "Referencia del cobro con tarjeta: "+rc.Ref, 9, false, 0)
	d.Line(9)
	d.Space(6)
	d.Rule()
	d.Text(0, "Factura (pedido)", 9, true, 300)
	d.TextRight(516, "Importe pagado", 9, true)
	d.Line(9)
	d.Rule()
	for _, x := range rc.Invoices {
		d.Text(0, "Pedido #"+strconv.FormatInt(x.Number, 10), 10, false, 300)
		d.TextRight(516, pdfdoc.Money(x.Amount, rc.Currency), 10, false)
		d.Line(10)
	}
	d.Rule()
	d.TextRight(400, "Total pagado", 11, true)
	d.TextRight(516, pdfdoc.Money(rc.Amount, rc.Currency), 11, true)
	d.Line(11)
	d.Space(10)
	d.Paragraph(0, "Este recibo comprueba el pago en línea de las facturas listadas. No sustituye al comprobante fiscal.", 8, 0)
	return d.Bytes()
}

func (s *Server) companyReceiptPDF(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	c, _, _, err := s.Orders.Company(r.Context(), p.Tenant, r.PathValue("id"), false)
	if err != nil {
		apierr.NotFound(w, "Empresa no encontrada")
		return
	}
	rc, err := s.Orders.ReceiptByID(r.Context(), p.Tenant, c.ID, r.PathValue("rid"))
	if err != nil {
		apierr.NotFound(w, "Recibo no encontrado")
		return
	}
	writePDF(w, fmt.Sprintf("recibo-%s.pdf", rc.ID), s.receiptPDF(p.Tenant, c.Name, rc))
}

func (s *Server) shopReceiptPDF(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.portalCompany(w, r)
	if !ok {
		return
	}
	rc, err := s.Orders.ReceiptByID(r.Context(), r.PathValue("tenant"), c.ID, r.PathValue("rid"))
	if err != nil {
		apierr.NotFound(w, "Recibo no encontrado")
		return
	}
	writePDF(w, fmt.Sprintf("recibo-%s.pdf", rc.ID), s.receiptPDF(r.PathValue("tenant"), c.Name, rc))
}
