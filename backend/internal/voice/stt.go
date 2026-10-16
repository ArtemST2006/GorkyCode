package voice

import (
	"context"
	"fmt"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

func ConvertToText(ctx context.Context, r *Recognizer, path string) (string, error) {
	wave := sherpa.ReadWave(path)
	if wave == nil {
		return "", fmt.Errorf("stt: failed to read wave %q", path)
	}

	stream := sherpa.NewOfflineStream(r.rec)
	defer sherpa.DeleteOfflineStream(stream)

	stream.AcceptWaveform(wave.SampleRate, wave.Samples)

	r.rec.Decode(stream)
	result := stream.GetResult()
	if result == nil {
		return "", fmt.Errorf("stt: failed to get result form stream")
	}

	return result.Text, nil
}
