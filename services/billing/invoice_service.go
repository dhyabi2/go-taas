package billing

import (
	"context"
	"strings"
	"time"

	billingv1 "github.com/go-taas/go-taas/proto/taas/billing/v1"
	commonv1 "github.com/go-taas/go-taas/proto/taas/common/v1"

	apierrors "github.com/go-taas/go-taas/pkg/errors"
)

// GenerateInvoice creates an invoice for an organization's billing
// period (feature-14 FR2.1, AC4). The bill is computed from the charge
// records of the UTC month starting at period_start. Idempotent: one
// invoice per (org, period).
func (s *Service) GenerateInvoice(ctx context.Context, req *billingv1.GenerateInvoiceRequest) (*billingv1.GenerateInvoiceResponse, error) {
	orgID := strings.TrimSpace(req.GetOrganizationId())
	if orgID == "" {
		return nil, apierrors.New(apierrors.CodeBillNotFound)
	}
	periodStart := req.GetPeriodStart()
	if periodStart <= 0 {
		return nil, apierrors.New(apierrors.CodeBillNotFound)
	}
	monthStart := monthStartOf(time.Unix(periodStart, 0).UTC())
	periodEnd := monthStart + int64(30*24*3600)

	// Compute the bill from the charge records of the period.
	repo, err := s.repository()
	if err != nil {
		return nil, err
	}
	rows, _, err := repo.BillSummaries(ctx, orgID, monthStart, periodEnd, 0, 1)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, apierrors.New(apierrors.CodeBillNotFound)
	}
	bill := rows[0]
	totalCents := centsFromAmount(bill.Amount)
	currency := bill.Currency
	if currency == "" {
		currency = "USD"
	}

	invRepo, err := s.invoiceRepository()
	if err != nil {
		return nil, err
	}
	inv, err := invRepo.GenerateFromBill(ctx, orgID+":"+itoa(monthStart), orgID, monthStart, periodEnd, totalCents, currency)
	if err != nil {
		return nil, err
	}
	return &billingv1.GenerateInvoiceResponse{Response: okResponse(), Invoice: summarizeInvoice(inv)}, nil
}

// ListInvoices returns invoices, newest first (feature-14 FR2.1).
func (s *Service) ListInvoices(ctx context.Context, req *billingv1.ListInvoicesRequest) (*billingv1.ListInvoicesResponse, error) {
	invRepo, err := s.invoiceRepository()
	if err != nil {
		return nil, err
	}
	offset, limit := normalizePagination(req.GetPage())
	rows, total, err := invRepo.ListInvoices(ctx, req.GetOrganizationId(), offset, limit)
	if err != nil {
		return nil, err
	}
	invoices := make([]*billingv1.Invoice, 0, len(rows))
	for _, row := range rows {
		invoices = append(invoices, summarizeInvoice(row))
	}
	return &billingv1.ListInvoicesResponse{
		Response: okResponse(),
		Invoices: invoices,
		PageMeta: &commonv1.PageMeta{Total: total, Offset: int64(offset), Limit: clampInt32(limit)},
	}, nil
}

// GetInvoice returns one invoice (feature-14 FR2.1).
func (s *Service) GetInvoice(ctx context.Context, req *billingv1.GetInvoiceRequest) (*billingv1.GetInvoiceResponse, error) {
	invRepo, err := s.invoiceRepository()
	if err != nil {
		return nil, err
	}
	inv, err := invRepo.FindInvoiceByID(ctx, req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	if inv == nil {
		return nil, apierrors.New(apierrors.CodeInvoiceNotFound)
	}
	return &billingv1.GetInvoiceResponse{Response: okResponse(), Invoice: summarizeInvoice(inv)}, nil
}

// DownloadInvoice returns a presentable rendering of an invoice
// (feature-14 FR2.2, AC5).
func (s *Service) DownloadInvoice(ctx context.Context, req *billingv1.DownloadInvoiceRequest) (*billingv1.DownloadInvoiceResponse, error) {
	invRepo, err := s.invoiceRepository()
	if err != nil {
		return nil, err
	}
	inv, err := invRepo.FindInvoiceByID(ctx, req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	if inv == nil {
		return nil, apierrors.New(apierrors.CodeInvoiceNotFound)
	}
	return &billingv1.DownloadInvoiceResponse{
		Response:    okResponse(),
		Content:     DownloadContent(inv),
		ContentType: "application/json",
	}, nil
}

// summarizeInvoice maps an invoice row to the proto summary.
func summarizeInvoice(inv *Invoice) *billingv1.Invoice {
	paidAt := int64(0)
	if inv.PaidAt != nil {
		paidAt = inv.PaidAt.Unix()
	}
	return &billingv1.Invoice{
		InvoiceId:      inv.ID,
		BillId:         inv.BillID,
		OrganizationId: inv.OrganizationID,
		PeriodStart:    inv.PeriodStart,
		PeriodEnd:      inv.PeriodEnd,
		TotalCents:     inv.TotalCents,
		Currency:       inv.Currency,
		Status:         inv.Status,
		IssuedAt:       inv.IssuedAt.Unix(),
		PaidAt:         paidAt,
	}
}
