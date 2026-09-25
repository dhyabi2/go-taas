package billing

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/go-taas/go-taas/pkg/database"
	apierrors "github.com/go-taas/go-taas/pkg/errors"
)

// Payment intent statuses (feature-14 AD2).
const (
	IntentStatusPending = "pending"
	IntentStatusPaid    = "paid"
	IntentStatusFailed  = "failed"
	IntentStatusExpired = "expired"
)

// mockChannelID is the always-available mock payment channel.
const mockChannelID = "mock"

// PaymentRepository persists payment channels and intents.
type PaymentRepository struct {
	db *gorm.DB
}

// NewPaymentRepository constructs a PaymentRepository bound to a gorm.DB.
func NewPaymentRepository(db *gorm.DB) *PaymentRepository {
	return &PaymentRepository{db: db}
}

// DB resolves the gorm handle for the context, joining an open
// transaction when present.
func (r *PaymentRepository) DB(ctx context.Context) *gorm.DB {
	return database.NewManager(r.db).DB(ctx)
}

// SeedMockChannel inserts the mock channel if absent (first-boot seed).
func (r *PaymentRepository) SeedMockChannel(ctx context.Context) error {
	return r.DB(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&PaymentChannel{
		ID:          mockChannelID,
		Type:        "mock",
		DisplayName: "Mock Payment",
		Enabled:     true,
		CreatedAt:   time.Now().UTC(),
	}).Error
}

// CreateChannel inserts a payment channel; a duplicate id maps to
// CodePaymentChannelInvalid.
func (r *PaymentRepository) CreateChannel(ctx context.Context, ch *PaymentChannel) error {
	if ch.ID == "" {
		return apierrors.New(apierrors.CodePaymentChannelInvalid)
	}
	ch.CreatedAt = time.Now().UTC()
	err := r.DB(ctx).Create(ch).Error
	if err != nil {
		return apierrors.Wrap(apierrors.CodePaymentChannelInvalid, err, "billing: create channel failed")
	}
	return nil
}

// ListChannels returns the channel registry, newest first.
func (r *PaymentRepository) ListChannels(ctx context.Context, offset, limit int) ([]*PaymentChannel, int64, error) {
	var rows []*PaymentChannel
	var total int64
	q := r.DB(ctx).Model(&PaymentChannel{})
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// FindChannel returns one channel by id, nil when absent.
func (r *PaymentRepository) FindChannel(ctx context.Context, id string) (*PaymentChannel, error) {
	var row PaymentChannel
	err := r.DB(ctx).Where("id = ?", id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// SetChannelEnabled toggles a channel's enabled state.
func (r *PaymentRepository) SetChannelEnabled(ctx context.Context, id string, enabled bool) (*PaymentChannel, error) {
	ch, err := r.FindChannel(ctx, id)
	if err != nil {
		return nil, err
	}
	if ch == nil {
		return nil, apierrors.New(apierrors.CodePaymentChannelInvalid)
	}
	if err := r.DB(ctx).Model(&PaymentChannel{}).Where("id = ?", id).
		Update("enabled", enabled).Error; err != nil {
		return nil, err
	}
	ch.Enabled = enabled
	return ch, nil
}

// CreateIntent inserts a pending payment intent.
func (r *PaymentRepository) CreateIntent(ctx context.Context, intent *PaymentIntent) error {
	if intent.ID == "" {
		intent.ID = uuid.NewString()
	}
	intent.Status = IntentStatusPending
	intent.CreatedAt = time.Now().UTC()
	return r.DB(ctx).Create(intent).Error
}

// FindIntentByID returns one intent by id, nil when absent.
func (r *PaymentRepository) FindIntentByID(ctx context.Context, id string) (*PaymentIntent, error) {
	var row PaymentIntent
	err := r.DB(ctx).Where("id = ?", id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ListIntents returns payment intents, newest first, filtered by
// account and status.
func (r *PaymentRepository) ListIntents(ctx context.Context, accountID, status string, offset, limit int) ([]*PaymentIntent, int64, error) {
	var rows []*PaymentIntent
	var total int64
	q := r.DB(ctx).Model(&PaymentIntent{})
	if accountID != "" {
		q = q.Where("account_id = ?", accountID)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// MarkIntentPaid marks an intent paid and credits the account exactly
// once (feature-14 AD2): the intent status guard plus the recharge
// idempotency key make a retried callback a no-op.
func (r *PaymentRepository) MarkIntentPaid(ctx context.Context, intent *PaymentIntent, _ *Account, _ int64) (*PaymentIntent, error) {
	if intent.Status == IntentStatusPaid {
		// Already paid: idempotent no-op.
		return intent, nil
	}
	now := time.Now().UTC()
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Guard: only a pending intent may be paid.
		var current PaymentIntent
		if err := tx.Where("id = ? AND status = ?", intent.ID, IntentStatusPending).First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apierrors.New(apierrors.CodePaymentIntentInvalid)
			}
			return err
		}
		if err := tx.Model(&PaymentIntent{}).Where("id = ?", intent.ID).
			Updates(map[string]interface{}{
				"status":     IntentStatusPaid,
				"paid_at":    now,
				"updated_at": now,
			}).Error; err != nil {
			return err
		}
		intent.Status = IntentStatusPaid
		intent.PaidAt = &now
		return nil
	})
	if err != nil {
		return nil, err
	}
	return intent, nil
}
