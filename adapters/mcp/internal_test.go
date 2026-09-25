package mcp

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// pages answers with n items per page, for as many pages as it is asked.
func pagesOf(perPage, lastPage int) func(context.Context, string) ([]int, string, error) {
	return func(_ context.Context, cursor string) ([]int, string, error) {
		n, _ := strconv.Atoi(cursor)
		items := make([]int, perPage)
		if lastPage > 0 && n+1 >= lastPage {
			return items, "", nil
		}
		return items, strconv.Itoa(n + 1), nil
	}
}

// TestReadAllBounds: the bounds on pages and on entries, at the input where
// removing each one changes the answer.
func TestReadAllBounds(t *testing.T) {
	ctx := context.Background()
	if got, err := readAll(ctx, pagesOf(1, maxListPages)); err != nil || len(got) != maxListPages {
		t.Errorf("a list of exactly %d pages: %d items, %v", maxListPages, len(got), err)
	}
	if _, err := readAll(ctx, pagesOf(1, maxListPages+1)); !errors.Is(err, ErrListBound) {
		t.Errorf("a list of %d pages = %v, want ErrListBound", maxListPages+1, err)
	}
	if _, err := readAll(ctx, pagesOf(1, 0)); !errors.Is(err, ErrListBound) {
		t.Errorf("an endless list = %v, want ErrListBound", err)
	}
	if got, err := readAll(ctx, pagesOf(maxListEntries, 1)); err != nil || len(got) != maxListEntries {
		t.Errorf("a page of exactly %d entries: %d items, %v", maxListEntries, len(got), err)
	}
	if _, err := readAll(ctx, pagesOf(maxListEntries+1, 1)); !errors.Is(err, ErrListBound) {
		t.Errorf("a page of %d entries = %v, want ErrListBound", maxListEntries+1, err)
	}
	// Two pages that pass the bound together are refused too.
	if _, err := readAll(ctx, pagesOf(maxListEntries/2+1, 2)); !errors.Is(err, ErrListBound) {
		t.Errorf("two pages over the bound = %v, want ErrListBound", err)
	}
}

// TestListCacheDropsAnOlderGeneration: a list shaped from a manifest older
// than the last refresh is never kept, and one shaped after it is.
func TestListCacheDropsAnOlderGeneration(t *testing.T) {
	now := time.Now()
	c := newListCache(time.Minute)
	tools := []*mcp.Tool{{Name: "t"}}
	c.put("alice", tools, 1, now)
	if _, ok := c.get("alice", now); !ok {
		t.Fatal("a list shaped from the current manifest was not kept")
	}
	c.reset(2)
	if _, ok := c.get("alice", now); ok {
		t.Error("a refresh left a list cached")
	}
	c.put("alice", tools, 1, now)
	if _, ok := c.get("alice", now); ok {
		t.Error("a list shaped from an older manifest was kept")
	}
	c.put("alice", tools, 2, now)
	if _, ok := c.get("alice", now); !ok {
		t.Error("a list shaped after the refresh was not kept")
	}
}

// TestListCacheIsBounded: the number of principals whose lists are kept is
// bounded, and an expired list makes room for a new one.
func TestListCacheIsBounded(t *testing.T) {
	now := time.Now()
	c := newListCache(time.Minute)
	tools := []*mcp.Tool{{Name: "t"}}
	for i := range maxCachedLists {
		c.put(strconv.Itoa(i), tools, 1, now)
	}
	if c.len() != maxCachedLists {
		t.Fatalf("the cache holds %d lists, want %d", c.len(), maxCachedLists)
	}
	c.put("one-too-many", tools, 1, now)
	if _, ok := c.get("one-too-many", now); ok || c.len() != maxCachedLists {
		t.Errorf("the cache grew past its bound: %d lists", c.len())
	}
	later := now.Add(2 * time.Minute)
	c.put("after-they-expire", tools, 1, later)
	if _, ok := c.get("after-they-expire", later); !ok {
		t.Errorf("an expired list did not make room: %d lists", c.len())
	}
}
