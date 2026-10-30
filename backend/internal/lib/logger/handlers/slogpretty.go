// Package slogpretty раскрашивает логи slog для локального окружения.
package slogpretty

import (
	"context"
	"encoding/json"
	"io"
	stdLog "log"
	"log/slog"
	"slices"

	"github.com/fatih/color"
)

// PrettyHandlerOptions — настройки PrettyHandler.
type PrettyHandlerOptions struct {
	SlogOpts *slog.HandlerOptions
}

// PrettyHandler — slog.Handler, который пишет цветной человекочитаемый вывод.
type PrettyHandler struct {
	slog.Handler
	l      *stdLog.Logger
	attrs  []slog.Attr
	groups []string
}

// NewPrettyHandler создает PrettyHandler, пишущий в out.
func (opts PrettyHandlerOptions) NewPrettyHandler(
	out io.Writer,
) *PrettyHandler {
	h := &PrettyHandler{
		Handler: slog.NewJSONHandler(out, opts.SlogOpts),
		l:       stdLog.New(out, "", 0),
	}

	return h
}

// Handle печатает запись: время, цветной уровень, сообщение и атрибуты в JSON.
func (h *PrettyHandler) Handle(_ context.Context, r slog.Record) error {
	level := r.Level.String() + ":"

	switch r.Level {
	case slog.LevelDebug:
		level = color.MagentaString(level)
	case slog.LevelInfo:
		level = color.BlueString(level)
	case slog.LevelWarn:
		level = color.YellowString(level)
	case slog.LevelError:
		level = color.RedString(level)
	}

	fields := make(map[string]any, len(h.attrs)+r.NumAttrs())
	for _, a := range h.attrs {
		addAttr(fields, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		addAttr(fields, groupAttr(a, h.groups))
		return true
	})

	var b []byte
	var err error

	if len(fields) > 0 {
		b, err = json.MarshalIndent(fields, "", "  ")
		if err != nil {
			return err
		}
	}

	timeStr := r.Time.Format("[15:04:05.000]")
	msg := color.CyanString(r.Message)

	h.l.Println(
		timeStr,
		level,
		msg,
		color.WhiteString(string(b)),
	)

	return nil
}

func addAttr(fields map[string]any, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Value.Kind() == slog.KindGroup {
		group, ok := fields[a.Key].(map[string]any)
		if !ok {
			group = make(map[string]any)
			fields[a.Key] = group
		}
		for _, child := range a.Value.Group() {
			addAttr(group, child)
		}
		return
	}
	fields[a.Key] = a.Value.Any()
}

func groupAttr(a slog.Attr, groups []string) slog.Attr {
	for _, group := range slices.Backward(groups) {
		a = slog.Group(group, a)
	}
	return a
}

// WithAttrs возвращает обработчик, добавляющий attrs к каждой записи.
func (h *PrettyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	combined := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	combined = append(combined, h.attrs...)
	for _, a := range attrs {
		combined = append(combined, groupAttr(a, h.groups))
	}
	return &PrettyHandler{
		Handler: h.Handler,
		l:       h.l,
		attrs:   combined,
		groups:  h.groups,
	}
}

// WithGroup возвращает обработчик с группой name.
func (h *PrettyHandler) WithGroup(name string) slog.Handler {
	groups := make([]string, 0, len(h.groups)+1)
	groups = append(groups, h.groups...)
	groups = append(groups, name)
	return &PrettyHandler{
		Handler: h.Handler,
		l:       h.l,
		attrs:   h.attrs,
		groups:  groups,
	}
}
