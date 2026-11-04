package config

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type scopedCredit struct {
	Origin string `json:"origin"`
	Wallet string `json:"wallet"`
	Token  string `json:"token"`
}

func creditPath(origin, wallet string) (string, string, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", "", fmt.Errorf("invalid API origin")
	}
	canonical := strings.ToLower(u.Scheme + "://" + u.Host)
	dir, err := configDir()
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(canonical + "\n" + strings.ToLower(wallet)))
	return filepath.Join(dir, "credit-"+hex.EncodeToString(sum[:])+".json"), canonical, nil
}

// Credit credentials are bound to the API origin and wallet that issued them.
func LoadCreditTokenFor(origin, wallet string) string {
	path, canonical, err := creditPath(origin, wallet)
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) && canonical == DefaultBaseURL {
		token := LoadCreditToken()
		parts := strings.Split(token, ".")
		if len(parts) == 3 {
			raw, decodeErr := base64.RawURLEncoding.DecodeString(parts[1])
			var claims struct {
				Sub string `json:"sub"`
			}
			if decodeErr == nil && json.Unmarshal(raw, &claims) == nil && wallet != "" && strings.EqualFold(claims.Sub, wallet) {
				if SaveCreditTokenFor(origin, wallet, token) == nil {
					ClearCreditToken()
				}
				return token
			}
		}
	}
	if err != nil {
		return ""
	}
	var record scopedCredit
	if json.Unmarshal(data, &record) != nil || record.Origin != canonical || !strings.EqualFold(record.Wallet, wallet) {
		return ""
	}
	return record.Token
}
func SaveCreditTokenFor(origin, wallet, token string) error {
	path, canonical, err := creditPath(origin, wallet)
	if err != nil {
		return err
	}
	data, err := json.Marshal(scopedCredit{canonical, wallet, token})
	if err != nil {
		return err
	}
	return WritePrivateFile(path, data)
}
func ClearCreditTokenFor(origin, wallet string) {
	path, _, err := creditPath(origin, wallet)
	if err == nil {
		_ = os.Remove(path)
	}
}

// Atomic replacement also corrects an existing file's permissions.
func WritePrivateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".credential-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}
