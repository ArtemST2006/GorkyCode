// Package voice provides speech recognition on top of the sherpa-onnx runtime.
package voice

import (
	"context"
	"fmt"
	"io"
	"os"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

// Recognizer wraps the sherpa-onnx offline recognizer.
type Recognizer struct {
	rec *sherpa.OfflineRecognizer
}

// NewModel loads the ASR model located at cfg.ModelPath.
func NewModel(cfg *Config) (*Recognizer, error) {
	if cfg == nil {
		return nil, fmt.Errorf("recognizer: check config (cfg is nil)")
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("recognizer: failed to validate config")
	}

	settings := sherpa.OfflineRecognizerConfig{
		FeatConfig: sherpa.FeatureConfig{
			SampleRate: 16000,
			FeatureDim: 80,
		},
		ModelConfig: sherpa.OfflineModelConfig{
			Transducer: sherpa.OfflineTransducerModelConfig{
				Encoder: cfg.ModelPath + "/encoder.onnx",
				Decoder: cfg.ModelPath + "/decoder.onnx",
				Joiner:  cfg.ModelPath + "/joiner.onnx",
			},
			Tokens:     cfg.ModelPath + "/tokens.txt",
			Provider:   cfg.Provider,
			NumThreads: cfg.NumThreads,
		},
		DecodingMethod: cfg.DecodeMethod,
	}

	recognizer := sherpa.NewOfflineRecognizer(&settings)

	return &Recognizer{rec: recognizer}, nil
}

// Close releases the native recognizer resources.
func (r *Recognizer) Close() {
	sherpa.DeleteOfflineRecognizer(r.rec)
}

// Transcribe reads a WAV stream and returns the recognized text.
func (r *Recognizer) Transcribe(ctx context.Context, wav io.Reader) (string, error) {
	if wav == nil {
		return "", fmt.Errorf("recognizer: file is nil")
	}

	f, err := os.CreateTemp("", "tmpfile-*.wav")
	if err != nil {
		return "", fmt.Errorf("recognizer: failed to create tmp file: %w", err)
	}
	defer func() {
		_ = os.Remove(f.Name())
		_ = f.Close()
	}()

	if _, err = io.Copy(f, wav); err != nil {
		return "", fmt.Errorf("recognizer: failed to write wav to tmp file: %w", err)
	}

	result, err := ConvertToText(ctx, r, f.Name())
	if err != nil {
		return "", fmt.Errorf("%w", err)
	}

	return result, nil
}
