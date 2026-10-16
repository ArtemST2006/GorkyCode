package voice

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTranscribeTestWavs(t *testing.T) {
	modelDir := "../../model/asr-model"
	if _, err := os.Stat(filepath.Join(modelDir, "tokens.txt")); os.IsNotExist(err) {
		t.Skip("model not found, run 'make download-model' first")
	}

	rec, err := NewModel(&VoiceConfig{
		ModelPath:    modelDir,
		NumThreads:   2,
		DecodeMethod: "greedy_search",
		Provider:     "cpu",
	})
	if err != nil {
		t.Fatalf("failed to load model: %v", err)
	}
	defer rec.Close()

	testFiles := []string{
		filepath.Join(modelDir, "test_wavs/0.wav"),
		filepath.Join(modelDir, "test_wavs/1.wav"),
	}

	for _, wavPath := range testFiles {
		t.Run(filepath.Base(wavPath), func(t *testing.T) {
			file, err := os.Open(wavPath)
			if err != nil {
				t.Fatalf("failed to open wav: %v", err)
			}
			defer file.Close()

			text, err := rec.Transcribe(context.Background(), file)
			if err != nil {
				t.Fatalf("transcribe failed: %v", err)
			}

			t.Logf("Audio: %s -> Recognized: %q", filepath.Base(wavPath), text)
			if text == "" {
				t.Errorf("expected non-empty recognized text")
			}
		})
	}
}
