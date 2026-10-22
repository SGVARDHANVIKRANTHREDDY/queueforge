package domain_test

import (
	"bytes"
	"testing"
	"time"

	"queueforge/internal/domain"
)

func TestCanonicalizeJSON_KeySorting(t *testing.T) {
	// Two JSON strings with identical keys in different orders
	jsonA := []byte(`{"z": 100, "a": "hello", "m": [3, 2, 1]}`)
	jsonB := []byte(`{"a": "hello", "m": [3, 2, 1], "z": 100}`)

	canonicalA, err := domain.CanonicalizeJSON(jsonA)
	if err != nil {
		t.Fatalf("Failed to canonicalize jsonA: %v", err)
	}

	canonicalB, err := domain.CanonicalizeJSON(jsonB)
	if err != nil {
		t.Fatalf("Failed to canonicalize jsonB: %v", err)
	}

	if !bytes.Equal(canonicalA, canonicalB) {
		t.Errorf("Canonical representations do not match:\nA: %s\nB: %s", string(canonicalA), string(canonicalB))
	}
}

func TestComputeCanonicalRequestHash_Equivalence(t *testing.T) {
	payload1 := []byte(`{"company":"Google", "priority_tier": 1}`)
	payload2 := []byte(`{"priority_tier": 1, "company":"Google"}`)

	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	hash1, err := domain.ComputeRequestHash(
		"notice.send",
		"notifications",
		payload1,
		5,
		now,
		true,
		5,
		300,
	)
	if err != nil {
		t.Fatalf("hash1 error: %v", err)
	}

	hash2, err := domain.ComputeRequestHash(
		"notice.send",
		"notifications",
		payload2,
		5,
		now,
		true,
		5,
		300,
	)
	if err != nil {
		t.Fatalf("hash2 error: %v", err)
	}

	if !bytes.Equal(hash1, hash2) {
		t.Errorf("Hashes for semantically identical requests differed!\nHash1: %x\nHash2: %x", hash1, hash2)
	}
}

func TestComputeCanonicalRequestHash_SentinelImmediate(t *testing.T) {
	payload := []byte(`{"msg": "test"}`)

	// When isExplicitRunAt is false, sentinel "immediate" must be hashed so time-drift doesn't alter hash
	hashImmediate1, err := domain.ComputeRequestHash("task", "default", payload, 5, time.Now(), false, 5, 300)
	if err != nil {
		t.Fatalf("hashImmediate1 error: %v", err)
	}

	// Wait briefly or use different timestamp
	hashImmediate2, err := domain.ComputeRequestHash("task", "default", payload, 5, time.Now().Add(5*time.Minute), false, 5, 300)
	if err != nil {
		t.Fatalf("hashImmediate2 error: %v", err)
	}

	if !bytes.Equal(hashImmediate1, hashImmediate2) {
		t.Errorf("Immediate hashes differed across calls!")
	}
}
