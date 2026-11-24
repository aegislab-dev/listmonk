package webhooks

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"math/big"
	"testing"
)

// A blocked bounce is a temporary block: soft, so that the address is not refused for good (LST-56).
func TestSendgridBlockedIsSoft(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	sg, err := NewSendgrid(base64.StdEncoding.EncodeToString(pub))
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`[{"email":"temporary@ok.test","event":"bounce","type":"blocked","bounce_classification":"Reputation","timestamp":1700000000},` +
		`{"email":"gone@ok.test","event":"bounce","type":"bounce","bounce_classification":"Invalid Address","timestamp":1700000000}]`)
	ts := "1700000000"
	h := sha256.Sum256(append([]byte(ts), body...))
	r, s, err := ecdsa.Sign(rand.Reader, key, h[:])
	if err != nil {
		t.Fatal(err)
	}
	sig, _ := asn1.Marshal(struct{ R, S *big.Int }{r, s})
	out, err := sg.ProcessBounce(base64.StdEncoding.EncodeToString(sig), ts, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].Type != "soft" || out[1].Type != "hard" {
		t.Fatalf("%+v", out)
	}
}
