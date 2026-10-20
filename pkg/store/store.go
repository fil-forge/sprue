package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// Page is a generic type representing a paginated response from the store.
type Page[T any] struct {
	Cursor  *string
	Results []T
}

type PaginationConfig struct {
	// Cursor is an optional string that indicates where to start the page. This
	// is typically the ID of the last item from the previous page.
	Cursor *string
	// Limit is an optional integer that specifies the maximum number of items to
	// return in the page. If not provided, a default limit may be applied by the
	// implementation.
	Limit *int
}

type GetPageFunc[T any] func(ctx context.Context, options PaginationConfig) (Page[T], error)

func Collect[T any](ctx context.Context, getPage GetPageFunc[T]) ([]T, error) {
	var items []T
	paginationOptions := PaginationConfig{}
	i := 0
	for {
		page, err := getPage(ctx, paginationOptions)
		if err != nil {
			return nil, fmt.Errorf("getting page %d: %w", i, err)
		}
		items = append(items, page.Results...)
		if page.Cursor == nil || len(page.Results) == 0 {
			break
		}
		paginationOptions.Cursor = page.Cursor
		i++
	}
	return items, nil
}

// Diff logs are listed in (receiptAt, cause) order, and a cursor carries both
// so a page boundary falling inside a group of changes sharing a timestamp
// resumes within that group rather than skipping the rest of it. Every diff log
// and every backend uses this encoding, so a cursor means the same thing
// whichever one issued it.
type cursorPayload struct {
	ReceiptAt time.Time `json:"r"`
	Cause     string    `json:"c"`
}

// EncodeCursor builds the cursor that resumes listing after the given change.
func EncodeCursor(receiptAt time.Time, cause string) string {
	b, _ := json.Marshal(cursorPayload{ReceiptAt: receiptAt.UTC(), Cause: cause})
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor reads back the change a cursor resumes after.
func DecodeCursor(cursor string) (time.Time, string, error) {
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", err
	}
	var p cursorPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return time.Time{}, "", err
	}
	return p.ReceiptAt, p.Cause, nil
}
