package handlers

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	"gorkycode/backend/internal/http-server/apierr"
	"gorkycode/backend/internal/lib/logger"
)

// TranscribeAudio returns recognized speech without running the task pipeline.
func (s *Server) TranscribeAudio(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticate(w, r); !ok {
		return
	}
	if s.transcriber == nil {
		apierr.Write(w, r, apierr.Unavailable("Распознавание речи временно недоступно."))
		return
	}

	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		apierr.Write(w, r, apierr.InvalidRequest("Ожидается multipart/form-data с WAV-файлом в поле file."))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxTaskProcessReadSize)
	if err := r.ParseMultipartForm(maxTaskProcessReadSize); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			apierr.Write(w, r, apierr.PayloadTooLarge())
		} else {
			apierr.Write(w, r, apierr.InvalidRequest("Некорректный multipart-запрос."))
		}
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	if len(r.MultipartForm.Value) != 0 || len(r.MultipartForm.File) != 1 || len(r.MultipartForm.File["file"]) != 1 {
		apierr.Write(w, r, apierr.InvalidRequest("Ожидается ровно один файл в поле file без других полей."))
		return
	}
	header := r.MultipartForm.File["file"][0]
	if header.Size == 0 || validateFile(header) != nil {
		apierr.Write(w, r, apierr.InvalidRequest("Поле file должно содержать непустой WAV-файл (.wav или .wave)."))
		return
	}
	file, err := header.Open()
	if err != nil {
		s.log.ErrorContext(r.Context(), "open uploaded audio", logger.Err(err))
		apierr.Write(w, r, apierr.Internal())
		return
	}
	defer func() { _ = file.Close() }()
	var signature [12]byte
	if _, err := io.ReadFull(file, signature[:]); err != nil || string(signature[:4]) != "RIFF" || string(signature[8:]) != "WAVE" {
		apierr.Write(w, r, apierr.InvalidRequest("Файл не содержит WAV-аудио."))
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		s.log.ErrorContext(r.Context(), "rewind uploaded audio", logger.Err(err))
		apierr.Write(w, r, apierr.Internal())
		return
	}

	text, err := s.transcriber.Transcribe(r.Context(), file)
	if err != nil {
		s.log.ErrorContext(r.Context(), "transcribe audio", logger.Err(err))
		if errors.Is(err, context.DeadlineExceeded) {
			apierr.Write(w, r, apierr.GatewayTimeout())
		} else {
			apierr.Write(w, r, apierr.TranscriptionFailed())
		}
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		apierr.Write(w, r, apierr.UnrecognizableAudio())
		return
	}
	if err := writeJSON(w, http.StatusOK, struct {
		Text string `json:"text"`
	}{Text: text}); err != nil {
		s.log.ErrorContext(r.Context(), "write transcription", slog.Any("error", err))
	}
}
