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

// NewModel loads the ASR model located at cfg.ModelPath. It supports both
// the legacy Zipformer transducer build and GigaAM as a single-file NeMo CTC
// model, selecting the recognizer configuration for the files actually present.
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
		},
		ModelConfig: sherpa.OfflineModelConfig{
			Tokens:     cfg.ModelPath + "/tokens.txt",
			Provider:   cfg.Provider,
			NumThreads: cfg.NumThreads,
		},
		DecodingMethod: cfg.DecodeMethod,
	}

	switch {
	case fileExists(cfg.ModelPath+"/encoder.int8.onnx") &&
		fileExists(cfg.ModelPath+"/decoder.int8.onnx") &&
		fileExists(cfg.ModelPath+"/joiner.int8.onnx"):
		// Legacy Zipformer transducer (quantized int8).
		settings.FeatConfig.FeatureDim = 80
		settings.ModelConfig.Transducer = sherpa.OfflineTransducerModelConfig{
			Encoder: cfg.ModelPath + "/encoder.int8.onnx",
			Decoder: cfg.ModelPath + "/decoder.int8.onnx",
			Joiner:  cfg.ModelPath + "/joiner.int8.onnx",
		}
	case fileExists(cfg.ModelPath+"/encoder.onnx") &&
		fileExists(cfg.ModelPath+"/decoder.onnx") &&
		fileExists(cfg.ModelPath+"/joiner.onnx"):
		// Legacy Zipformer transducer (fp32 fallback).
		settings.FeatConfig.FeatureDim = 80
		settings.ModelConfig.Transducer = sherpa.OfflineTransducerModelConfig{
			Encoder: cfg.ModelPath + "/encoder.onnx",
			Decoder: cfg.ModelPath + "/decoder.onnx",
			Joiner:  cfg.ModelPath + "/joiner.onnx",
		}
	case fileExists(cfg.ModelPath + "/model.int8.onnx"):
		// GigaAM v3 (NeMo CTC), int8.
		settings.FeatConfig.FeatureDim = 64
		settings.ModelConfig.NemoCTC = sherpa.OfflineNemoEncDecCtcModelConfig{
			Model: cfg.ModelPath + "/model.int8.onnx",
		}
		settings.ModelConfig.ModelType = "nemo_ctc"
	case fileExists(cfg.ModelPath + "/model.onnx"):
		// GigaAM v3 (NeMo CTC), fp32.
		settings.FeatConfig.FeatureDim = 64
		settings.ModelConfig.NemoCTC = sherpa.OfflineNemoEncDecCtcModelConfig{
			Model: cfg.ModelPath + "/model.onnx",
		}
		settings.ModelConfig.ModelType = "nemo_ctc"
	default:
		return nil, fmt.Errorf("recognizer: no supported model files found in %q", cfg.ModelPath)
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

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
