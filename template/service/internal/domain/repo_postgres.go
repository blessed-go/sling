package domain

import (
	"context"
	"errors"

	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repo struct {
	pg *pgxpool.Pool
}

func NewRepo(pg *pgxpool.Pool) *Repo {
	return &Repo{pg: pg}
}

func (r *Repo) Get(ctx context.Context, id uuid.UUID) (*Item, error) {
	query := `SELECT id, title, created_at FROM items WHERE id = $1`
	var item Item
	err := r.pg.QueryRow(ctx, query, id).Scan(&item.ID, &item.Title, &item.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrItemNotFound
		}
		return nil, err
	}
	return &item, nil
}

func (r *Repo) List(ctx context.Context) ([]Item, error) {
	query := `SELECT id, title, created_at FROM items ORDER BY created_at DESC LIMIT 100`
	rows, err := r.pg.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Item
	for rows.Next() {
		var item Item
		if err := rows.Scan(&item.ID, &item.Title, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (r *Repo) Create(ctx context.Context, item *Item) error {
	query := `INSERT INTO items (id, title, created_at) VALUES ($1, $2, $3)`
	_, err := r.pg.Exec(ctx, query, item.ID, item.Title, item.CreatedAt)
	return err
}
