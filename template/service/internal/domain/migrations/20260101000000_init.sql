-- +goose Up
CREATE TABLE IF NOT EXISTS items (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    title TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_items_created_at ON items(created_at);

-- +goose Down
DROP TABLE IF EXISTS items;