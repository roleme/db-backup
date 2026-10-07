package driver

import (
	"errors"
	"fmt"
	"os"

	"github.com/roleme/db-backup/internal/config"
)

func tlsKeys(cfg *config.Config) (ca, fingerprint string, err error) {
	ca, fingerprint = cfg.Get("DB_SSL_CA"), cfg.Get("DB_SSL_FINGERPRINT")
	if ca != "" && fingerprint != "" {
		return "", "", errors.New("set only one of DB_SSL_CA and DB_SSL_FINGERPRINT")
	}
	if ca != "" {
		f, err := os.Open(ca)
		if err != nil {
			return "", "", fmt.Errorf("DB_SSL_CA %s is not readable", ca)
		}
		f.Close()
	}
	return ca, fingerprint, nil
}

func rejectTLSKeys(cfg *config.Config, driver string) error {
	for _, key := range []string{"DB_SSL_CA", "DB_SSL_FINGERPRINT"} {
		if cfg.Get(key) != "" {
			return fmt.Errorf("%s does not apply to %s", key, driver)
		}
	}
	return nil
}
