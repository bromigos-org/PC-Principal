package brodec

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// headerTimestamp holds the unix seconds a delivery was signed at, and
	// headerSignature the hex Ed25519 signature over that timestamp followed
	// by the body.
	headerTimestamp = "X-Brodec-Timestamp"
	headerSignature = "X-Brodec-Signature"

	// replayWindow is how far a delivery's timestamp may be from now.
	replayWindow = 5 * time.Minute
)

var (
	errNoKey        = errors.New("the gateway's public key is not there yet")
	errUnsigned     = errors.New("the timestamp or signature header is missing")
	errStale        = errors.New("the timestamp is outside the 5 minute window")
	errBadSignature = errors.New("the signature does not verify")
)

// parsePublicKey reads the gateway's public key as the gateway writes it:
// base64, with or without padding.
func parsePublicKey(text string) (ed25519.PublicKey, error) {
	text = strings.TrimSpace(text)
	decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(text, "="))
	if err != nil {
		return nil, fmt.Errorf("the gateway's public key is not base64: %w", err)
	}
	if len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("the gateway's public key is %d bytes, not %d", len(decoded), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(decoded), nil
}

// verifier checks that a delivery was signed by the gateway.
//
// The key is either fixed (from the environment) or read from the file the
// gateway writes at every start. The file is read when it is first needed
// and read again when a signature does not verify, so that the bot may
// start before the gateway has written it, and follows a gateway that came
// back with a new key.
type verifier struct {
	path string
	now  func() time.Time

	mu       sync.Mutex
	key      ed25519.PublicKey
	fixed    bool
	loadedAt time.Time
}

// keyReloadEvery bounds how often a signature that does not verify makes the
// verifier read the key file again.
const keyReloadEvery = time.Second

func newVerifier(fixedKey string, path string, now func() time.Time) (*verifier, error) {
	v := &verifier{path: path, now: now}
	if strings.TrimSpace(fixedKey) != "" {
		key, err := parsePublicKey(fixedKey)
		if err != nil {
			return nil, err
		}
		v.key, v.fixed = key, true
	}
	return v, nil
}

// hasKey reports whether a key is in hand, loading it from the file if it
// was not.
func (v *verifier) hasKey() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key == nil {
		v.load()
	}
	return v.key != nil
}

// load reads the key file. It keeps the key in hand when the file is
// missing or unreadable. The caller holds the lock.
func (v *verifier) load() {
	v.loadedAt = v.now()
	text, err := os.ReadFile(v.path)
	if err != nil {
		return
	}
	if key, err := parsePublicKey(string(text)); err == nil {
		v.key = key
	}
}

// verify checks one delivery: both headers present, the timestamp within
// the window, and the signature good for timestamp + body.
func (v *verifier) verify(timestamp string, signatureHex string, body []byte) error {
	if timestamp == "" || signatureHex == "" {
		return errUnsigned
	}
	seconds, err := strconv.ParseUint(timestamp, 10, 64)
	if err != nil {
		return errStale
	}
	if seconds > 1<<62 {
		return errStale
	}
	age := v.now().Unix() - int64(seconds)
	if window := int64(replayWindow / time.Second); age > window || -age > window {
		return errStale
	}
	signature, err := hex.DecodeString(signatureHex)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return errBadSignature
	}
	message := append([]byte(timestamp), body...)

	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key == nil {
		v.load()
	}
	if v.key == nil {
		return errNoKey
	}
	if ed25519.Verify(v.key, message, signature) {
		return nil
	}
	if v.fixed || v.now().Sub(v.loadedAt) < keyReloadEvery {
		return errBadSignature
	}
	v.load()
	if ed25519.Verify(v.key, message, signature) {
		return nil
	}
	return errBadSignature
}

// matches reports whether text is the key the verifier checks with. A fixed
// key or a stale file may differ from what the gateway signs with; reading
// the file again settles the second case.
func (v *verifier) matches(text string) bool {
	key, err := parsePublicKey(text)
	if err != nil {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.fixed && !key.Equal(v.key) {
		v.load()
	}
	return key.Equal(v.key)
}
