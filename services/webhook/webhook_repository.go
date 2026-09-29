package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/go-taas/go-taas/pkg/database"
	apierrors "github.com/go-taas/go-taas/pkg/errors"
)

// Repository persists webhooks and their deliveries on top of the
// generic repository base (feature #23).
type Repository struct {
	*database.BaseRepository[Webhook]
	db *database.Manager
}

// NewRepository constructs a Repository bound to a database Manager.
func NewRepository(db *gorm.DB) *Repository {
	mgr := database.NewManager(db)
	return &Repository{
		BaseRepository: database.NewBaseRepository[Webhook](mgr),
		db:             mgr,
	}
}

// InsertWebhook inserts a webhook and returns the generated id (AC1).
func (r *Repository) InsertWebhook(ctx context.Context, wh *Webhook) (string, error) {
	if wh.WebhookID == "" {
		wh.WebhookID = uuid.NewString()
	}
	if wh.CreatedAt.IsZero() {
		wh.CreatedAt = time.Now().UTC()
	}
	wh.UpdatedAt = wh.CreatedAt
	if err := r.DB(ctx).Create(wh).Error; err != nil {
		return "", apierrors.Wrap(apierrors.CodeInternal, err, "webhook: insert failed")
	}
	return wh.WebhookID, nil
}

// FindWebhookByID returns the webhook with the given id scoped to the
// org. A miss maps to 10701 (AC1).
func (r *Repository) FindWebhookByID(ctx context.Context, orgID, webhookID string) (*Webhook, error) {
	if _, err := uuid.Parse(webhookID); err != nil {
		return nil, apierrors.New(apierrors.CodeWebhookNotFound)
	}
	var row Webhook
	err := r.DB(ctx).First(&row, "webhook_id = ? AND organization_id = ?", webhookID, orgID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apierrors.New(apierrors.CodeWebhookNotFound)
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ListWebhooks returns one page of webhooks for the org, newest first,
// with the delivery summary, and the total count (AC3).
func (r *Repository) ListWebhooks(ctx context.Context, filter WebhookFilter) ([]*Webhook, int64, error) {
	query := r.DB(ctx).Model(&Webhook{}).Where("organization_id = ?", filter.OrganizationID)
	if filter.Name != "" {
		query = query.Where("name LIKE ?", "%"+filter.Name+"%")
	}
	if filter.EnabledFilter {
		query = query.Where("enabled = ?", filter.Enabled)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []*Webhook
	if err := query.
		Order("created_at DESC").Order("webhook_id DESC").
		Offset(filter.Offset).Limit(filter.Limit).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// UpdateWebhook persists the mutable config fields of a webhook.
func (r *Repository) UpdateWebhook(ctx context.Context, wh *Webhook) error {
	wh.UpdatedAt = time.Now().UTC()
	return r.DB(ctx).Model(&Webhook{}).
		Where("webhook_id = ? AND organization_id = ?", wh.WebhookID, wh.OrganizationID).
		Updates(map[string]any{
			"name":                wh.Name,
			"url":                 wh.URL,
			"enabled_event_types": wh.EnabledEventTypes,
			"max_attempts":        wh.MaxAttempts,
			"backoff_seconds":     wh.BackoffSeconds,
			"updated_at":          wh.UpdatedAt,
		}).Error
}

// DeleteWebhook deletes a webhook and its delivery log (cascade, AC4).
func (r *Repository) DeleteWebhook(ctx context.Context, orgID, webhookID string) error {
	if _, err := uuid.Parse(webhookID); err != nil {
		return apierrors.New(apierrors.CodeWebhookNotFound)
	}
	res := r.DB(ctx).Where("webhook_id = ? AND organization_id = ?", webhookID, orgID).Delete(&Webhook{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return apierrors.New(apierrors.CodeWebhookNotFound)
	}
	// Cascade delete the delivery log.
	return r.DB(ctx).Where("webhook_id = ?", webhookID).Delete(&WebhookDelivery{}).Error
}

// SetWebhookEnabled toggles the enabled state of a webhook (AC4).
func (r *Repository) SetWebhookEnabled(ctx context.Context, orgID, webhookID string, enabled bool) (*Webhook, error) {
	wh, err := r.FindWebhookByID(ctx, orgID, webhookID)
	if err != nil {
		return nil, err
	}
	wh.Enabled = enabled
	wh.UpdatedAt = time.Now().UTC()
	if err := r.DB(ctx).Model(&Webhook{}).
		Where("webhook_id = ? AND organization_id = ?", webhookID, orgID).
		Updates(map[string]any{"enabled": enabled, "updated_at": wh.UpdatedAt}).Error; err != nil {
		return nil, err
	}
	return wh, nil
}

// RollWebhookSecret replaces the encrypted secret of a webhook (AC5).
func (r *Repository) RollWebhookSecret(ctx context.Context, orgID, webhookID string, ciphertext []byte) (*Webhook, error) {
	wh, err := r.FindWebhookByID(ctx, orgID, webhookID)
	if err != nil {
		return nil, err
	}
	wh.SecretCiphertext = ciphertext
	wh.UpdatedAt = time.Now().UTC()
	if err := r.DB(ctx).Model(&Webhook{}).
		Where("webhook_id = ? AND organization_id = ?", webhookID, orgID).
		Updates(map[string]any{"secret_ciphertext": ciphertext, "updated_at": wh.UpdatedAt}).Error; err != nil {
		return nil, err
	}
	return wh, nil
}

// FindWebhooksForEvent returns the enabled webhooks of an org that
// subscribe to the given event type (AC8).
func (r *Repository) FindWebhooksForEvent(ctx context.Context, orgID, eventType string) ([]*Webhook, error) {
	var rows []*Webhook
	if err := r.DB(ctx).
		Where("organization_id = ? AND enabled = ?", orgID, true).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	var matched []*Webhook
	for _, wh := range rows {
		types, err := wh.EventTypes()
		if err != nil {
			continue
		}
		for _, t := range types {
			if t == eventType {
				matched = append(matched, wh)
				break
			}
		}
	}
	return matched, nil
}

// InsertDelivery inserts a delivery row and returns the generated id.
func (r *Repository) InsertDelivery(ctx context.Context, d *WebhookDelivery) (string, error) {
	if d.DeliveryID == "" {
		d.DeliveryID = uuid.NewString()
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	if err := r.DB(ctx).Create(d).Error; err != nil {
		return "", apierrors.Wrap(apierrors.CodeInternal, err, "webhook: delivery insert failed")
	}
	return d.DeliveryID, nil
}

// FindDeliveryByID returns a delivery scoped to the org and webhook. A
// miss maps to 10704 (AC7).
func (r *Repository) FindDeliveryByID(ctx context.Context, orgID, webhookID, deliveryID string) (*WebhookDelivery, error) {
	if _, err := uuid.Parse(deliveryID); err != nil {
		return nil, apierrors.New(apierrors.CodeWebhookDeliveryNotFound)
	}
	var row WebhookDelivery
	err := r.DB(ctx).First(&row,
		"delivery_id = ? AND webhook_id = ? AND organization_id = ?",
		deliveryID, webhookID, orgID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apierrors.New(apierrors.CodeWebhookDeliveryNotFound)
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ListDeliveries returns one page of deliveries for a webhook, newest
// first, and the total count (AC7).
func (r *Repository) ListDeliveries(ctx context.Context, filter DeliveryFilter) ([]*WebhookDelivery, int64, error) {
	query := r.DB(ctx).Model(&WebhookDelivery{}).
		Where("webhook_id = ? AND organization_id = ?", filter.WebhookID, filter.OrganizationID)
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.EventType != "" {
		query = query.Where("event_type = ?", filter.EventType)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []*WebhookDelivery
	if err := query.
		Order("created_at DESC").Order("delivery_id DESC").
		Offset(filter.Offset).Limit(filter.Limit).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// UpdateDelivery persists the outcome of a delivery attempt.
func (r *Repository) UpdateDelivery(ctx context.Context, d *WebhookDelivery) error {
	return r.DB(ctx).Model(&WebhookDelivery{}).
		Where("delivery_id = ?", d.DeliveryID).
		Updates(map[string]any{
			"status":           d.Status,
			"http_status_code": d.HTTPStatusCode,
			"attempt_count":    d.AttemptCount,
			"failure_reason":   d.FailureReason,
			"next_attempt_at":  d.NextAttemptAt,
			"last_attempt_at":  d.LastAttemptAt,
		}).Error
}

// FindDueDeliveries returns up to limit pending deliveries whose
// next_attempt_at is due (AC8).
func (r *Repository) FindDueDeliveries(ctx context.Context, now time.Time, limit int) ([]*WebhookDelivery, error) {
	if limit <= 0 {
		limit = 100
	}
	var rows []*WebhookDelivery
	if err := r.DB(ctx).
		Where("status = ? AND next_attempt_at IS NOT NULL AND next_attempt_at <= ?", DeliveryStatusPending, now.UTC()).
		Order("next_attempt_at ASC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// DeleteDeliveriesBefore deletes up to batch deliveries older than the
// cutoff and returns the number deleted (AC8, AD8).
func (r *Repository) DeleteDeliveriesBefore(ctx context.Context, cutoff time.Time, batch int) (int64, error) {
	if batch <= 0 {
		batch = 1
	}
	var ids []string
	if err := r.DB(ctx).Model(&WebhookDelivery{}).
		Where("created_at < ?", cutoff.UTC()).
		Limit(batch).
		Pluck("delivery_id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	result := r.DB(ctx).Where("delivery_id IN ?", ids).Delete(&WebhookDelivery{})
	return result.RowsAffected, result.Error
}

// DeliverySummary returns the total/delivered/failed counts for a
// webhook (AC3).
func (r *Repository) DeliverySummary(ctx context.Context, webhookID string) (total, delivered, failed int64, err error) {
	var t, d, f int64
	if err := r.DB(ctx).Model(&WebhookDelivery{}).Where("webhook_id = ?", webhookID).Count(&t).Error; err != nil {
		return 0, 0, 0, err
	}
	if err := r.DB(ctx).Model(&WebhookDelivery{}).Where("webhook_id = ? AND status = ?", webhookID, DeliveryStatusDelivered).Count(&d).Error; err != nil {
		return 0, 0, 0, err
	}
	if err := r.DB(ctx).Model(&WebhookDelivery{}).Where("webhook_id = ? AND status = ?", webhookID, DeliveryStatusFailed).Count(&f).Error; err != nil {
		return 0, 0, 0, err
	}
	return t, d, f, nil
}

// EventTypes decodes the JSON enabled_event_types column.
func (w *Webhook) EventTypes() ([]string, error) {
	var types []string
	if err := json.Unmarshal([]byte(w.EnabledEventTypes), &types); err != nil {
		return nil, err
	}
	return types, nil
}

// SetEventTypes encodes the enabled event types into the JSON column.
func (w *Webhook) SetEventTypes(types []string) error {
	raw, err := json.Marshal(types)
	if err != nil {
		return err
	}
	w.EnabledEventTypes = string(raw)
	return nil
}
