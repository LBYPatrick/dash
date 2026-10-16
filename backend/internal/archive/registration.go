package archive

import (
	"encoding/base64"
	"encoding/json"
)

// BundledRegistration is supplied by release packaging from CD secrets. It
// identifies the distributed application, never a user's account. Installed
// application client secrets cannot be confidential inside a public binary.
var BundledRegistration string

type registration struct {
	GoogleClientID     string `json:"googleClientId"`
	GoogleClientSecret string `json:"googleClientSecret"`
	MicrosoftClientID  string `json:"microsoftClientId"`
}

func bundledRegistration() registration {
	var value registration
	data, err := base64.RawStdEncoding.DecodeString(BundledRegistration)
	if err == nil {
		_ = json.Unmarshal(data, &value)
	}
	return value
}
func BundledProviders() map[string]bool {
	value := bundledRegistration()
	return map[string]bool{"gdrive": value.GoogleClientID != "" && value.GoogleClientSecret != "", "onedrive": value.MicrosoftClientID != ""}
}
func withRegistration(c Config) Config {
	if c.ClientID != "" {
		return c
	}
	value := bundledRegistration()
	switch c.Kind {
	case "gdrive":
		c.ClientID, c.Secret.ClientSecret = value.GoogleClientID, value.GoogleClientSecret
	case "onedrive":
		c.ClientID = value.MicrosoftClientID
	}
	return c
}
