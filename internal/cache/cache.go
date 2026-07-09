// Package cache provides in-memory semantic result memoization for split execution.
package cache

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/ricardocabral/ajq/internal/backend"
)

// DefaultModelID is the stable model identity used until real model backends are introduced.
const DefaultModelID = "ajq-default-model"

// Key is an opaque, unambiguous semantic cache identity.
type Key string

// Store is a semantic result cache with an in-memory front and optional
// persistent disk backing. NewStore remains memory-only; persistent storage is
// enabled through the constructors in disk.go.
type Store struct {
	mu      sync.RWMutex
	results map[Key]backend.Result
	disk    *diskStore
}

// NewStore creates an empty in-memory semantic cache.
func NewStore() *Store {
	return &Store{results: make(map[Key]backend.Result)}
}

// Get returns the cached result for key.
func (s *Store) Get(key Key) (backend.Result, bool) {
	if s == nil {
		return backend.Result{}, false
	}
	s.mu.RLock()
	result, ok := s.results[key]
	s.mu.RUnlock()
	if ok {
		return result, true
	}
	if s.disk == nil {
		return backend.Result{}, false
	}
	result, ok = s.disk.get(key)
	if !ok {
		return backend.Result{}, false
	}
	s.mu.Lock()
	s.results[key] = result
	s.mu.Unlock()
	return result, true
}

// Set stores result for key.
func (s *Store) Set(key Key, result backend.Result) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.results[key] = result
	s.mu.Unlock()
	if s.disk != nil && result.Error == "" {
		s.disk.set(key, result)
	}
}

// KeyForJudgement builds the semantic cache key for one judgement.
func KeyForJudgement(j backend.Judgement) (Key, error) {
	modelID := j.ModelID
	if modelID == "" {
		modelID = DefaultModelID
	}
	value, err := CanonicalValue(j.Value)
	if err != nil {
		return "", fmt.Errorf("canonicalize semantic value: %w", err)
	}
	parts := struct {
		Op      string          `json:"op"`
		Specs   []string        `json:"spec"`
		ModelID string          `json:"model_id"`
		Value   json.RawMessage `json:"value"`
	}{Op: j.Op, Specs: append([]string(nil), j.Specs...), ModelID: modelID, Value: json.RawMessage(value)}
	encoded, err := json.Marshal(parts)
	if err != nil {
		return "", fmt.Errorf("encode semantic cache key: %w", err)
	}
	return Key(encoded), nil
}

// CanonicalValue returns deterministic JSON for values used in semantic cache keys.
func CanonicalValue(value any) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeCanonical(&buf, value); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, value any) error {
	switch v := value.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if v {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		encoded, err := json.Marshal(v)
		if err != nil {
			return err
		}
		buf.Write(encoded)
	case int:
		buf.WriteString(strconv.FormatInt(int64(v), 10))
	case int8:
		buf.WriteString(strconv.FormatInt(int64(v), 10))
	case int16:
		buf.WriteString(strconv.FormatInt(int64(v), 10))
	case int32:
		buf.WriteString(strconv.FormatInt(int64(v), 10))
	case int64:
		buf.WriteString(strconv.FormatInt(v, 10))
	case uint:
		buf.WriteString(strconv.FormatUint(uint64(v), 10))
	case uint8:
		buf.WriteString(strconv.FormatUint(uint64(v), 10))
	case uint16:
		buf.WriteString(strconv.FormatUint(uint64(v), 10))
	case uint32:
		buf.WriteString(strconv.FormatUint(uint64(v), 10))
	case uint64:
		buf.WriteString(strconv.FormatUint(v, 10))
	case float32:
		return writeFloat(buf, float64(v), 32)
	case float64:
		return writeFloat(buf, v, 64)
	case json.Number:
		normalized, err := normalizeJSONNumber(v)
		if err != nil {
			return err
		}
		buf.WriteString(normalized)
	case *big.Int:
		if v == nil {
			buf.WriteString("null")
		} else {
			buf.WriteString(v.String())
		}
	case []any:
		return writeCanonicalArray(buf, v)
	case map[string]any:
		return writeCanonicalObject(buf, v)
	default:
		return fmt.Errorf("unsupported cache key value type %T", value)
	}
	return nil
}

func writeCanonicalArray(buf *bytes.Buffer, values []any) error {
	buf.WriteByte('[')
	for i, value := range values {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := writeCanonical(buf, value); err != nil {
			return err
		}
	}
	buf.WriteByte(']')
	return nil
}

func writeCanonicalObject(buf *bytes.Buffer, values map[string]any) error {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	buf.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return err
		}
		buf.Write(encodedKey)
		buf.WriteByte(':')
		if err := writeCanonical(buf, values[key]); err != nil {
			return err
		}
	}
	buf.WriteByte('}')
	return nil
}

func writeFloat(buf *bytes.Buffer, value float64, bitSize int) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("unsupported non-finite number %v", value)
	}
	if value == 0 {
		buf.WriteByte('0')
		return nil
	}
	buf.WriteString(strconv.FormatFloat(value, 'g', -1, bitSize))
	return nil
}

func normalizeJSONNumber(value json.Number) (string, error) {
	text := value.String()
	if text == "" {
		return "", fmt.Errorf("invalid json number %q", text)
	}

	sign := ""
	switch text[0] {
	case '-':
		sign = "-"
		text = text[1:]
	case '+':
		return "", fmt.Errorf("invalid json number %q", value.String())
	}

	exponent := big.NewInt(0)
	if idx := strings.IndexAny(text, "eE"); idx >= 0 {
		if _, ok := exponent.SetString(text[idx+1:], 10); !ok {
			return "", fmt.Errorf("invalid json number %q", value.String())
		}
		text = text[:idx]
	}

	intPart, fracPart := text, ""
	if idx := strings.IndexByte(text, '.'); idx >= 0 {
		intPart, fracPart = text[:idx], text[idx+1:]
	}
	if intPart == "" || !allDigits(intPart) || (fracPart != "" && !allDigits(fracPart)) {
		return "", fmt.Errorf("invalid json number %q", value.String())
	}

	digits := strings.TrimLeft(intPart+fracPart, "0")
	if digits == "" {
		return "0", nil
	}
	decimalExp := new(big.Int).Sub(exponent, big.NewInt(int64(len(fracPart))))
	for strings.HasSuffix(digits, "0") {
		digits = digits[:len(digits)-1]
		decimalExp.Add(decimalExp, big.NewInt(1))
	}
	if decimalExp.Sign() == 0 {
		return sign + digits, nil
	}
	return sign + digits + "e" + decimalExp.String(), nil
}

func allDigits(text string) bool {
	if text == "" {
		return false
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
