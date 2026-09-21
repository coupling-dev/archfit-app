package auth

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

const (
	jwtHeader   = `{"alg":"RS256","typ":"JWT"}`
	jwtBackdate = 60 * time.Second // clock drift between us and GitHub
	jwtLifetime = 10 * time.Minute // from iat, so exp stays under GitHub's 10-minute limit
)

// AppJWT signs a fresh App JWT; none is cached.
func (s *source) AppJWT() (string, error) {
	iat := s.now().Add(-jwtBackdate)
	claims, err := json.Marshal(struct {
		Iss string `json:"iss"`
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
	}{strconv.FormatInt(s.appID, 10), iat.Unix(), iat.Add(jwtLifetime).Unix()})
	if err != nil {
		return "", errors.New(errPrefix + "encode app JWT claims")
	}
	enc := base64.RawURLEncoding
	signingInput := enc.EncodeToString([]byte(jwtHeader)) + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(nil, s.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", errors.New(errPrefix + "sign app JWT")
	}
	return signingInput + "." + enc.EncodeToString(sig), nil
}
