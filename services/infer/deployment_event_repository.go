package infer

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/go-taas/go-taas/pkg/database"
	apierrors "github.com/go-taas/go-taas/pkg/errors"
)

// DeploymentEventFilter is the query filter for the deployment event
// trail (feature #34, AD4).
type DeploymentEventFilter struct {
	ServiceID string
	EventType string
	Actor     string
	Since     int64
	Until     int64
}

// DeploymentEventRepository persists deployment events on top of the
// generic repository base (feature #34, AD2).
type DeploymentEventRepository struct {
	*database.BaseRepository[DeploymentEvent]
	db *database.Manager
}

// NewDeploymentEventRepository constructs a DeploymentEventRepository
// bound to a database Manager.
func NewDeploymentEventRepository(db *gorm.DB) *DeploymentEventRepository {
	mgr := database.NewManager(db)
	return &DeploymentEventRepository{
		BaseRepository: database.NewBaseRepository[DeploymentEvent](mgr),
		db:             mgr,
	}
}

// RecordEvent inserts a deployment event row (feature #34, AD2).
func (r *DeploymentEventRepository) RecordEvent(ctx context.Context, ev *DeploymentEvent) error {
	if ev.ID == "" {
		ev.ID = uuid.NewString()
	}
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now().UTC()
	}
	return r.Create(ctx, ev)
}

// ListEvents returns the trail newest first, filtered by service/event
// type/actor/time range, paginated (feature #34, AD4).
func (r *DeploymentEventRepository) ListEvents(ctx context.Context, filter DeploymentEventFilter, offset, limit int) ([]*DeploymentEvent, int64, error) {
	where := []string{}
	args := []any{}
	if filter.ServiceID != "" {
		where = append(where, "service_id = ?")
		args = append(args, filter.ServiceID)
	}
	if filter.EventType != "" {
		where = append(where, "event_type = ?")
		args = append(args, filter.EventType)
	}
	if filter.Actor != "" {
		where = append(where, "actor = ?")
		args = append(args, filter.Actor)
	}
	if filter.Since > 0 {
		where = append(where, "created_at >= ?")
		args = append(args, time.Unix(filter.Since, 0).UTC())
	}
	if filter.Until > 0 {
		where = append(where, "created_at <= ?")
		args = append(args, time.Unix(filter.Until, 0).UTC())
	}
	conds := []any{}
	if len(where) > 0 {
		conds = append(conds, strings.Join(where, " AND "))
		conds = append(conds, args...)
	}
	return r.Paginate(ctx, offset, limit, conds, "created_at DESC", "id DESC")
}

// GetEvent returns one event by id (feature #34, AD5). A miss maps to
// CodeInferEndpointNotFound (reused for an unknown deployment event).
func (r *DeploymentEventRepository) GetEvent(ctx context.Context, eventID string) (*DeploymentEvent, error) {
	var row DeploymentEvent
	err := r.DB(ctx).Where("id = ?", eventID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apierrors.New(apierrors.CodeInferEndpointNotFound)
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}
