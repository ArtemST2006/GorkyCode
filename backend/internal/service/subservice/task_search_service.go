package subservice

import (
	"context"
	"encoding/json"
	"fmt"
	"gorkycode/backend/internal/generated/ai"
	"gorkycode/backend/internal/model"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	api "gorkycode/backend/internal/generated"

	"github.com/google/uuid"
)

// SearchEvents combines literal title matches with compatible semantic matches.
func (l *Lister) SearchEvents(ctx context.Context, userID uuid.UUID, action json.RawMessage) (api.TaskCollection, error) {
	if userID == uuid.Nil {
		return api.TaskCollection{}, fmt.Errorf("%w: empty user_id", model.ErrInvalidAction)
	}
	var act ai.SearchEventsAction
	if err := json.Unmarshal(action, &act); err != nil {
		return api.TaskCollection{}, fmt.Errorf("%w: decode: %w", model.ErrInvalidAction, err)
	}
	query := strings.TrimSpace(act.Query)
	if query == "" {
		return api.TaskCollection{}, fmt.Errorf("%w: empty search query", model.ErrInvalidAction)
	}
	var from, to *time.Time
	if act.DateFrom != nil {
		from = &act.DateFrom.Time
		if from.IsZero() {
			return api.TaskCollection{}, fmt.Errorf("%w: invalid date_from", model.ErrInvalidAction)
		}
	}
	if act.DateToExclusive != nil {
		to = &act.DateToExclusive.Time
		if to.IsZero() {
			return api.TaskCollection{}, fmt.Errorf("%w: invalid date_to_exclusive", model.ErrInvalidAction)
		}
	}
	if from != nil && to != nil && !from.Before(*to) {
		return api.TaskCollection{}, fmt.Errorf("%w: invalid period", model.ErrInvalidAction)
	}
	types, err := parseRecordTypes(act.RecordTypes)
	if err != nil {
		return api.TaskCollection{}, err
	}
	tasks, err := l.store.SearchEvents(ctx, userID, query, from, to, types)
	if err != nil {
		return api.TaskCollection{}, fmt.Errorf("search tasks: %w", err)
	}
	if l.semanticStore != nil && l.embedder != nil {
		vector, embedErr := l.embedder.Embed(ctx, "query", query)
		if embedErr == nil && len(vector.Vector) == model.EmbeddingDimension &&
			strings.TrimSpace(vector.Model) != "" && strings.TrimSpace(vector.Version) != "" {
			semantic, findErr := l.semanticStore.FindSemantic(
				ctx, userID, vector.Vector, vector.Model, vector.Version, from, to, nil, nil, types,
			)
			if findErr == nil {
				tasks = mergeCandidates(tasks, filterNamedMatches(query, semantic))
			}
		}
	}
	items := make([]api.TaskItem, 0, len(tasks))
	for _, task := range tasks {
		item, err := toItem(task)
		if err != nil {
			return api.TaskCollection{}, err
		}
		items = append(items, item)
	}
	return api.TaskCollection{Items: items}, nil
}

// When a query names a person after «с/со», the embedding alone is too broad:
// different people in otherwise identical meeting titles have close vectors.
// Keep candidates with the same name or a close inflection/typo. For example,
// «с Иваном» must find the stored title «Встреча с Иванов».
func filterNamedMatches(query string, tasks []model.Task) []model.Task {
	words := strings.Fields(query)
	anchor := ""
	for i := 1; i < len(words); i++ {
		previous := strings.ToLower(strings.Trim(words[i-1], ".,!?«»\"'"))
		if previous != "с" && previous != "со" {
			continue
		}
		word := strings.Trim(words[i], ".,!?«»\"'")
		if utf8.RuneCountInString(word) >= 3 {
			anchor = strings.ToLower(word)
			break
		}
	}
	if anchor == "" {
		return tasks
	}
	filtered := make([]model.Task, 0, len(tasks))
	for _, task := range tasks {
		if titleMatchesName(task.Title, anchor) {
			filtered = append(filtered, task)
		}
	}
	return filtered
}

func titleMatchesName(title, name string) bool {
	nameRunes := []rune(name)
	for _, word := range strings.FieldsFunc(strings.ToLower(title), func(r rune) bool { return !unicode.IsLetter(r) }) {
		if word == name {
			return true
		}
		wordRunes := []rune(word)
		lengthDifference := len(nameRunes) - len(wordRunes)
		if lengthDifference < 0 {
			lengthDifference = -lengthDifference
		}
		if len(nameRunes) < 4 || len(wordRunes) < 4 || lengthDifference > 2 {
			continue
		}
		prefix := 0
		for prefix < len(nameRunes) && prefix < len(wordRunes) && nameRunes[prefix] == wordRunes[prefix] {
			prefix++
		}
		if prefix >= 3 && editDistanceWithinTwo(nameRunes, wordRunes) {
			return true
		}
	}
	return false
}

func editDistanceWithinTwo(a, b []rune) bool {
	previous := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i, left := range a {
		current := make([]int, len(b)+1)
		current[0] = i + 1
		rowBest := current[0]
		for j, right := range b {
			cost := 0
			if left != right {
				cost = 1
			}
			current[j+1] = min(previous[j+1]+1, current[j]+1, previous[j]+cost)
			rowBest = min(rowBest, current[j+1])
		}
		if rowBest > 2 {
			return false
		}
		previous = current
	}
	return previous[len(b)] <= 2
}
