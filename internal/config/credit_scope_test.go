package config

import (
	"encoding/base64"
	"os"
	"testing"
)

func TestCreditTokenIsolationAndPrivateStorage(t *testing.T) {
	t.Setenv("LAEVITAS_CONFIG_DIR", t.TempDir())
	const wallet = "0x0000000000000000000000000000000000000001"
	if err := SaveCreditTokenFor(DefaultBaseURL, wallet, "secret"); err != nil {
		t.Fatal(err)
	}
	if LoadCreditTokenFor("https://example.com", wallet) != "" || LoadCreditTokenFor(DefaultBaseURL, "another-wallet") != "" {
		t.Fatal("credential crossed origin/wallet boundary")
	}
	path, _, _ := creditPath(DefaultBaseURL, wallet)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("credential permissions %v", info.Mode())
	}
	ClearCreditTokenFor(DefaultBaseURL, wallet)
	if LoadCreditTokenFor(DefaultBaseURL, wallet) != "" {
		t.Fatal("credential survived logout")
	}
}
func TestLegacyTokenMigrationRequiresWalletAndProductionOrigin(t *testing.T) {
	t.Setenv("LAEVITAS_CONFIG_DIR", t.TempDir())
	const wallet = "0x0000000000000000000000000000000000000001"
	token := "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"`+wallet+`"}`)) + ".signature"
	if err := SaveCreditToken(token); err != nil {
		t.Fatal(err)
	}
	if LoadCreditTokenFor("https://example.com", wallet) != "" || LoadCreditTokenFor(DefaultBaseURL, "another-wallet") != "" {
		t.Fatal("legacy credential leaked")
	}
	if LoadCreditTokenFor(DefaultBaseURL, wallet) != token {
		t.Fatal("legacy wallet credit token not migrated")
	}
	ClearCreditTokenFor(DefaultBaseURL, wallet)
	if LoadCreditTokenFor(DefaultBaseURL, wallet) != "" {
		t.Fatal("legacy token resurrected after logout")
	}
}
