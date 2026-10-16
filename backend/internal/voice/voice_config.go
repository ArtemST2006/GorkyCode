package voice

import "fmt"

type VoiceConfig struct {
	ModelPath    string
	NumThreads   int
	DecodeMethod string
	Provider     string
}

func (c *VoiceConfig) validate() error {
	if c.ModelPath == "" {
		return fmt.Errorf("voice_config: Model path is required")
	}

	if c.NumThreads <= 0 {
		return fmt.Errorf("voice_config: numbers of threads are incorrect")
	}

	switch c.DecodeMethod {
	case "greedy_search":
	default:
		return fmt.Errorf("voice_config: unknown decode method")
	}

	switch c.Provider {
	case "cpu", "cuda":
	default:
		return fmt.Errorf("voice_config: unknown model provider")
	}

	return nil
}
