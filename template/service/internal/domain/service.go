package domain

import (
	"context"
	"log/slog"

	"uuid"
)

type Repository interface {
	Get(ctx context.Context, id uuid.UUID) (*Item, error)
	List(ctx context.Context) ([]Item, error)
	Create(ctx context.Context, item *Item) error
}

type Service struct {
	log  *slog.Logger
	repo Repository
}

func NewService(log *slog.Logger, repo Repository) *Service {
	return &Service{log: log, repo: repo}
}

func (s *Service) GetItem(ctx context.Context, id uuid.UUID) (*Item, error) {
	s.log.InfoContext(ctx, "fetching item", slog.String("id", id.String()))
	return s.repo.Get(ctx, id)
}

func (s *Service) ListItems(ctx context.Context) ([]Item, error) {
	return s.repo.List(ctx)
}

func (s *Service) CreateItem(ctx context.Context, title string) (*Item, error) {
	item, err := NewItem(title)
	if err != nil {
		return nil, err
	}

	if err := s.repo.Create(ctx, item); err != nil {
		return nil, err
	}

	s.log.InfoContext(ctx, "item created", slog.String("id", item.ID.String()))
	return item, nil
}
