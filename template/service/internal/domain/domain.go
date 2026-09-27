package domain

import (
	"errors"
	"net/http"
	"time"
	"uuid"
)

var (
	ErrItemNotFound = errors.New("item not found")
	ErrInvalidItem  = errors.New("invalid item title")
)

// ErrorMapping maps domain errors to their corresponding HTTP status codes.
var ErrorMapping = map[error]int{
	ErrItemNotFound: http.StatusNotFound,
	ErrInvalidItem:  http.StatusBadRequest,
}

type Item struct {
	ID        uuid.UUID `db:"id"`
	Title     string    `db:"title"`
	CreatedAt time.Time `db:"created_at"`
}

func NewItem(title string) (*Item, error) {
	if len(title) == 0 {
		return nil, ErrInvalidItem
	}

	id := uuid.NewV7()

	return &Item{
		ID:        id,
		Title:     title,
		CreatedAt: time.Now(),
	}, nil
}
