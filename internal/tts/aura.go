// Package tts synthesizes the synthetic caller's voice with Deepgram Aura.
//
// Synthesized audio is cached on disk keyed by (voice, sample rate, text).
// A load test that re-runs the same scenario 500 times should pay the
// synthesis cost once, not 500 times -- both to keep runs cheap and to keep
// the caller's audio bit-identical across runs so latency numbers stay
// comparable.
package tts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Utterances are also held in memory, process-wide, keyed the same way as the
// disk cache. Without this, 500 concurrent callers reading the same four
// utterances off disk hold 500 copies of identical audio. The returned slices
// are shared and must be treated as immutable -- the pump only ever copies out
// of them, never into them.
var (
	memMu    sync.RWMutex
	memCache = map[string][]byte{}
)

func memGet(key string) ([]byte, bool) {
	memMu.RLock()
	defer memMu.RUnlock()
	b, ok := memCache[key]
	return b, ok
}

func memPut(key string, pcm []byte) {
	memMu.Lock()
	defer memMu.Unlock()
	memCache[key] = pcm
}

const speakURL = "https://api.deepgram.com/v1/speak"

// Client synthesizes text to raw linear16 PCM at SampleRate.
type Client struct {
	APIKey     string
	SampleRate int
	CacheDir   string

	http *http.Client
}

func New(apiKey string, sampleRate int, cacheDir string) *Client {
	return &Client{
		APIKey:     apiKey,
		SampleRate: sampleRate,
		CacheDir:   cacheDir,
		http:       &http.Client{Timeout: 60 * time.Second},
	}
}

// Synthesize returns raw signed 16-bit little-endian mono PCM for text, and
// reports whether it came from cache. The returned slice is shared across
// callers and must not be modified.
func (c *Client) Synthesize(voice, text string) (pcm []byte, cached bool, err error) {
	key := cacheKey(voice, c.SampleRate, text)

	if b, ok := memGet(key); ok {
		return b, true, nil
	}

	path := filepath.Join(c.CacheDir, key+".pcm")
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		memPut(key, b)
		return b, true, nil
	}

	b, err := c.fetch(voice, text)
	if err != nil {
		return nil, false, err
	}
	memPut(key, b)

	if err := os.MkdirAll(c.CacheDir, 0o755); err == nil {
		// A failed cache write is not a failed synthesis.
		_ = os.WriteFile(path, b, 0o644)
	}
	return b, false, nil
}

func (c *Client) fetch(voice, text string) ([]byte, error) {
	body, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, speakURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	q := req.URL.Query()
	q.Set("model", voice)
	q.Set("encoding", "linear16")
	q.Set("sample_rate", fmt.Sprint(c.SampleRate))
	q.Set("container", "none")
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Authorization", "Token "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("aura tts: %s: %s", resp.Status, bytes.TrimSpace(msg))
	}

	pcm, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if len(pcm) == 0 {
		return nil, fmt.Errorf("aura tts: empty audio for %q", text)
	}
	return pcm, nil
}

func cacheKey(voice string, sampleRate int, text string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s", voice, sampleRate, text)))
	return hex.EncodeToString(sum[:8])
}
