package domain

import (
	"encoding/json"
	"net/http"
	"time"

	"uuid"

	"github.com/blessed-go/sling/platform/httpx"
	"github.com/go-chi/chi/v5"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

type ItemResponse struct {
	ID        uuid.UUID `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *Handler) GetItem(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.BadRequest(w, r, "invalid item id")
		return
	}

	item, err := h.svc.GetItem(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	httpx.JSON(w, http.StatusOK, ItemResponse{
		ID:        item.ID,
		Title:     item.Title,
		CreatedAt: item.CreatedAt,
	})
}

func (h *Handler) ListItems(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.ListItems(r.Context())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	resp := make([]ItemResponse, len(items))
	for i, item := range items {
		resp[i] = ItemResponse{
			ID:        item.ID,
			Title:     item.Title,
			CreatedAt: item.CreatedAt,
		}
	}
	httpx.JSON(w, http.StatusOK, resp)
}

type CreateItemRequest struct {
	Title string `json:"title"`
}

func (h *Handler) CreateItem(w http.ResponseWriter, r *http.Request) {
	var req CreateItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.BadRequest(w, r, "invalid request body")
		return
	}

	item, err := h.svc.CreateItem(r.Context(), req.Title)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	httpx.JSON(w, http.StatusCreated, ItemResponse{
		ID:        item.ID,
		Title:     item.Title,
		CreatedAt: item.CreatedAt,
	})
}
