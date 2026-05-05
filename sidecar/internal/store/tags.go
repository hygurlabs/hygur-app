// Package store provides SQLite database access for the Hygur knowledge base.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Tag represents a tag that can be applied to knowledge items.
type Tag struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`               // hex color like "#FF5733"
	AutoRule  string    `json:"auto_rule,omitempty"` // e.g., "folder:Documents", "mail:from:@company.com"
	IsAuto    bool      `json:"is_auto"`             // true if auto-generated, false if user-created
	ItemCount int       `json:"item_count"`          // computed, not stored
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TagColors defines the predefined color palette for tags.
var TagColors = []string{
	"#3B82F6", // blue
	"#10B981", // green
	"#F59E0B", // amber
	"#EF4444", // red
	"#8B5CF6", // violet
	"#EC4899", // pink
	"#06B6D4", // cyan
	"#F97316", // orange
}

// MaxAutoTags is the maximum number of auto-generated tags allowed.
const MaxAutoTags = 100

// DefaultTagColor returns a color from the palette based on the tag name hash.
func DefaultTagColor(name string) string {
	hash := 0
	for _, c := range name {
		hash = (hash*31 + int(c)) % len(TagColors)
	}
	if hash < 0 {
		hash = -hash
	}
	return TagColors[hash%len(TagColors)]
}

// ListTags returns all tags with their item counts.
func (d *DB) ListTags(ctx context.Context) ([]*Tag, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT
			t.id, t.name, t.color, t.auto_rule, t.is_auto, t.created_at, t.updated_at,
			COUNT(it.content_id) as item_count
		FROM tags t
		LEFT JOIN item_tags it ON t.id = it.tag_id
		GROUP BY t.id, t.name, t.color, t.auto_rule, t.is_auto, t.created_at, t.updated_at
		ORDER BY t.name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list tags: %w", err)
	}
	defer rows.Close()

	var tags []*Tag
	for rows.Next() {
		tag := &Tag{}
		var autoRule sql.NullString

		err := rows.Scan(
			&tag.ID,
			&tag.Name,
			&tag.Color,
			&autoRule,
			&tag.IsAuto,
			&tag.CreatedAt,
			&tag.UpdatedAt,
			&tag.ItemCount,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan tag: %w", err)
		}

		if autoRule.Valid {
			tag.AutoRule = autoRule.String
		}

		tags = append(tags, tag)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating tags: %w", err)
	}

	return tags, nil
}

// GetTag retrieves a tag by its ID.
func (d *DB) GetTag(ctx context.Context, id string) (*Tag, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT
			t.id, t.name, t.color, t.auto_rule, t.is_auto, t.created_at, t.updated_at,
			COUNT(it.content_id) as item_count
		FROM tags t
		LEFT JOIN item_tags it ON t.id = it.tag_id
		WHERE t.id = ?
		GROUP BY t.id, t.name, t.color, t.auto_rule, t.is_auto, t.created_at, t.updated_at
	`, id)

	tag := &Tag{}
	var autoRule sql.NullString

	err := row.Scan(
		&tag.ID,
		&tag.Name,
		&tag.Color,
		&autoRule,
		&tag.IsAuto,
		&tag.CreatedAt,
		&tag.UpdatedAt,
		&tag.ItemCount,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get tag: %w", err)
	}

	if autoRule.Valid {
		tag.AutoRule = autoRule.String
	}

	return tag, nil
}

// GetTagByName retrieves a tag by its name (case-insensitive).
func (d *DB) GetTagByName(ctx context.Context, name string) (*Tag, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT
			t.id, t.name, t.color, t.auto_rule, t.is_auto, t.created_at, t.updated_at,
			COUNT(it.content_id) as item_count
		FROM tags t
		LEFT JOIN item_tags it ON t.id = it.tag_id
		WHERE LOWER(t.name) = LOWER(?)
		GROUP BY t.id, t.name, t.color, t.auto_rule, t.is_auto, t.created_at, t.updated_at
	`, name)

	tag := &Tag{}
	var autoRule sql.NullString

	err := row.Scan(
		&tag.ID,
		&tag.Name,
		&tag.Color,
		&autoRule,
		&tag.IsAuto,
		&tag.CreatedAt,
		&tag.UpdatedAt,
		&tag.ItemCount,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get tag by name: %w", err)
	}

	if autoRule.Valid {
		tag.AutoRule = autoRule.String
	}

	return tag, nil
}

// CreateTag creates a new tag.
func (d *DB) CreateTag(ctx context.Context, tag *Tag) error {
	if tag.ID == "" {
		tag.ID = uuid.New().String()
	}
	if tag.Color == "" {
		tag.Color = DefaultTagColor(tag.Name)
	}

	now := time.Now()
	if tag.CreatedAt.IsZero() {
		tag.CreatedAt = now
	}
	tag.UpdatedAt = now

	var autoRule *string
	if tag.AutoRule != "" {
		autoRule = &tag.AutoRule
	}

	_, err := d.db.ExecContext(ctx, `
		INSERT INTO tags (id, name, color, auto_rule, is_auto, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, tag.ID, tag.Name, tag.Color, autoRule, tag.IsAuto, tag.CreatedAt, tag.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to create tag: %w", err)
	}

	return nil
}

// UpdateTag updates an existing tag.
func (d *DB) UpdateTag(ctx context.Context, tag *Tag) error {
	tag.UpdatedAt = time.Now()

	var autoRule *string
	if tag.AutoRule != "" {
		autoRule = &tag.AutoRule
	}

	result, err := d.db.ExecContext(ctx, `
		UPDATE tags
		SET name = ?, color = ?, auto_rule = ?, updated_at = ?
		WHERE id = ?
	`, tag.Name, tag.Color, autoRule, tag.UpdatedAt, tag.ID)
	if err != nil {
		return fmt.Errorf("failed to update tag: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("tag not found: %s", tag.ID)
	}

	return nil
}

// GetItemsForTag returns all knowledge items that have a specific tag.
func (d *DB) GetItemsForTag(ctx context.Context, tagID string) ([]*KnowledgeItem, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT ki.content_id, ki.source_type, ki.source_path, ki.title, ki.normalized_text,
		       ki.metadata, ki.version_id, ki.created_at, ki.updated_at
		FROM knowledge_items ki
		INNER JOIN item_tags it ON ki.content_id = it.content_id
		WHERE it.tag_id = ?
		ORDER BY ki.updated_at DESC
	`, tagID)
	if err != nil {
		return nil, fmt.Errorf("failed to get items for tag: %w", err)
	}
	defer rows.Close()

	var items []*KnowledgeItem
	for rows.Next() {
		var item KnowledgeItem
		var metadata string
		var sourcePath sql.NullString
		err := rows.Scan(
			&item.ContentID, &item.SourceType, &sourcePath, &item.Title,
			&item.NormalizedText, &metadata, &item.VersionID, &item.CreatedAt, &item.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan knowledge item: %w", err)
		}
		if sourcePath.Valid {
			sp := sourcePath.String
			item.SourcePath = &sp
		}
		if err := json.Unmarshal([]byte(metadata), &item.Metadata); err != nil {
			item.Metadata = make(map[string]any)
		}
		items = append(items, &item)
	}

	return items, nil
}

// DeleteTag deletes a tag and removes it from all items.
func (d *DB) DeleteTag(ctx context.Context, id string) error {
	result, err := d.db.ExecContext(ctx, "DELETE FROM tags WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete tag: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("tag not found: %s", id)
	}

	return nil
}

// GetTagsForItem returns all tags associated with a knowledge item.
func (d *DB) GetTagsForItem(ctx context.Context, contentID string) ([]*Tag, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT t.id, t.name, t.color, t.auto_rule, t.is_auto, t.created_at, t.updated_at
		FROM tags t
		INNER JOIN item_tags it ON t.id = it.tag_id
		WHERE it.content_id = ?
		ORDER BY t.name ASC
	`, contentID)
	if err != nil {
		return nil, fmt.Errorf("failed to get tags for item: %w", err)
	}
	defer rows.Close()

	var tags []*Tag
	for rows.Next() {
		tag := &Tag{}
		var autoRule sql.NullString

		err := rows.Scan(
			&tag.ID,
			&tag.Name,
			&tag.Color,
			&autoRule,
			&tag.IsAuto,
			&tag.CreatedAt,
			&tag.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan tag: %w", err)
		}

		if autoRule.Valid {
			tag.AutoRule = autoRule.String
		}

		tags = append(tags, tag)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating tags: %w", err)
	}

	return tags, nil
}

// AddTagToItem associates a tag with a knowledge item.
func (d *DB) AddTagToItem(ctx context.Context, contentID, tagID string) error {
	_, err := d.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO item_tags (content_id, tag_id, created_at)
		VALUES (?, ?, ?)
	`, contentID, tagID, time.Now())
	if err != nil {
		return fmt.Errorf("failed to add tag to item: %w", err)
	}

	return nil
}

// RemoveTagFromItem removes a tag association from a knowledge item.
func (d *DB) RemoveTagFromItem(ctx context.Context, contentID, tagID string) error {
	result, err := d.db.ExecContext(ctx, `
		DELETE FROM item_tags WHERE content_id = ? AND tag_id = ?
	`, contentID, tagID)
	if err != nil {
		return fmt.Errorf("failed to remove tag from item: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("tag not found on item")
	}

	return nil
}

// RemoveAllTagsFromItem removes all tag associations from a knowledge item.
func (d *DB) RemoveAllTagsFromItem(ctx context.Context, contentID string) error {
	_, err := d.db.ExecContext(ctx, `
		DELETE FROM item_tags WHERE content_id = ?
	`, contentID)
	if err != nil {
		return fmt.Errorf("failed to remove all tags from item: %w", err)
	}
	return nil
}

// GetOrCreateTag gets an existing tag by name or creates a new one.
// This is useful for auto-tagging where we want to reuse existing tags.
func (d *DB) GetOrCreateTag(ctx context.Context, name string, isAuto bool, autoRule string) (*Tag, error) {
	// Check if tag already exists (case-insensitive)
	existing, err := d.GetTagByName(ctx, name)
	if err != nil {
		return nil, err
	}

	if existing != nil {
		return existing, nil
	}

	// Create new tag
	tag := &Tag{
		ID:       uuid.New().String(),
		Name:     name,
		Color:    DefaultTagColor(name),
		IsAuto:   isAuto,
		AutoRule: autoRule,
	}

	if err := d.CreateTag(ctx, tag); err != nil {
		// Handle race condition: another goroutine might have created the tag
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			existing, err := d.GetTagByName(ctx, name)
			if err != nil {
				return nil, err
			}
			return existing, nil
		}
		return nil, err
	}

	return tag, nil
}

// CountAutoTags returns the number of auto-generated tags.
func (d *DB) CountAutoTags(ctx context.Context) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM tags WHERE is_auto = TRUE").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count auto tags: %w", err)
	}
	return count, nil
}

// GetLeastUsedAutoTags returns auto-generated tags with the lowest usage, for pruning.
func (d *DB) GetLeastUsedAutoTags(ctx context.Context, limit int) ([]*Tag, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT
			t.id, t.name, t.color, t.auto_rule, t.is_auto, t.created_at, t.updated_at,
			COUNT(it.content_id) as item_count
		FROM tags t
		LEFT JOIN item_tags it ON t.id = it.tag_id
		WHERE t.is_auto = TRUE
		GROUP BY t.id, t.name, t.color, t.auto_rule, t.is_auto, t.created_at, t.updated_at
		ORDER BY item_count ASC, t.created_at ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get least used auto tags: %w", err)
	}
	defer rows.Close()

	var tags []*Tag
	for rows.Next() {
		tag := &Tag{}
		var autoRule sql.NullString

		err := rows.Scan(
			&tag.ID,
			&tag.Name,
			&tag.Color,
			&autoRule,
			&tag.IsAuto,
			&tag.CreatedAt,
			&tag.UpdatedAt,
			&tag.ItemCount,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan tag: %w", err)
		}

		if autoRule.Valid {
			tag.AutoRule = autoRule.String
		}

		tags = append(tags, tag)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating tags: %w", err)
	}

	return tags, nil
}

// PruneAutoTags removes the least-used auto-generated tags to stay under MaxAutoTags.
func (d *DB) PruneAutoTags(ctx context.Context) error {
	count, err := d.CountAutoTags(ctx)
	if err != nil {
		return err
	}

	if count <= MaxAutoTags {
		return nil // No pruning needed
	}

	// Number of tags to remove
	toRemove := count - MaxAutoTags

	// Get tags to remove
	tags, err := d.GetLeastUsedAutoTags(ctx, toRemove)
	if err != nil {
		return err
	}

	// Delete each tag
	for _, tag := range tags {
		if err := d.DeleteTag(ctx, tag.ID); err != nil {
			// Log but continue
			continue
		}
	}

	return nil
}
