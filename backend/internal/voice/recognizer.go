package voice

import (
	"context"
	"fmt"
	"io"
	"os"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

type Recognizer struct {
	rec *sherpa.OfflineRecognizer
}

func NewModel(cfg *VoiceConfig) (*Recognizer, error) {
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

func (r *Recognizer) Close() {
	sherpa.DeleteOfflineRecognizer(r.rec)
}

func (r *Recognizer) Transcribe(ctx context.Context, wav io.Reader) (string, error) {
	if wav == nil {
		return "", fmt.Errorf("recognizer: file is nil")
	}

	f, err := os.CreateTemp("", "tmpfile-*.wav")
	if err != nil {
		return "", fmt.Errorf("recognizer: failed to create tmp file: %w", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	if _, err := io.Copy(f, wav); err != nil {
		return "", fmt.Errorf("recognizer: failed to write wav to tmp file: %w", err)
	}

	result, err := ConvertToText(ctx, r, f.Name())
	if err != nil {
		return "", fmt.Errorf("%w", err)
	}

	return result, nil
}
