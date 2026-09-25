package billing

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/go-taas/go-taas/pkg/database"
)

// InvoiceRepository persists invoices derived from settled bills
// (feature-14 AD3).
type InvoiceRepository struct {
	db *gorm.DB
}

// NewInvoiceRepository constructs an InvoiceRepository bound to a gorm.DB.
func NewInvoiceRepository(db *gorm.DB) *InvoiceRepository {
	return &InvoiceRepository{db: db}
}

// DB resolves the gorm handle for the context.
func (r *InvoiceRepository) DB(ctx context.Context) *gorm.DB {
	return database.NewManager(r.db).DB(ctx)
}

// GenerateFromBill creates an invoice from a bill's parameters
// (idempotent: one invoice per bill via the unique bill_id). Returns
// the existing invoice when the bill already has one.
func (r *InvoiceRepository) GenerateFromBill(ctx context.Context, billID, orgID string, periodStart, periodEnd, totalCents int64, currency string) (*Invoice, error) {
	var existing Invoice
	err := r.DB(ctx).Where("bill_id = ?", billID).First(&existing).Error
	if err == nil {
		return &existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	inv := &Invoice{
		ID:             uuid.NewString(),
		BillID:         billID,
		OrganizationID: orgID,
		PeriodStart:    periodStart,
		PeriodEnd:      periodEnd,
		TotalCents:     totalCents,
		Currency:       currency,
		Status:         "issued",
		IssuedAt:       time.Now().UTC(),
	}
	if err := r.DB(ctx).Create(inv).Error; err != nil {
		return nil, err
	}
	return inv, nil
}

// FindInvoiceByID returns one invoice by id, nil when absent.
func (r *InvoiceRepository) FindInvoiceByID(ctx context.Context, id string) (*Invoice, error) {
	var row Invoice
	err := r.DB(ctx).Where("id = ?", id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ListInvoices returns invoices, newest first, filtered by org.
func (r *InvoiceRepository) ListInvoices(ctx context.Context, orgID string, offset, limit int) ([]*Invoice, int64, error) {
	var rows []*Invoice
	var total int64
	q := r.DB(ctx).Model(&Invoice{})
	if orgID != "" {
		q = q.Where("organization_id = ?", orgID)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := q.Order("issued_at DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// DownloadContent renders an invoice as a presentable JSON document.
func DownloadContent(inv *Invoice) string {
	return "{\"invoice_id\":\"" + inv.ID + "\",\"bill_id\":\"" + inv.BillID +
		"\",\"organization_id\":\"" + inv.OrganizationID +
		"\",\"total_cents\":" + itoa(inv.TotalCents) +
		",\"currency\":\"" + inv.Currency + "\",\"status\":\"" + inv.Status + "\"}"
}

// itoa is a minimal int64-to-string helper for the download rendering.
func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
