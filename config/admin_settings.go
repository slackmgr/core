package config

type AdminSettings struct {
	GlobalAdmins  map[string]string              `json:"globalAdmins"`
	AlertChannels map[string]*AlertChannelConfig `json:"alertChannels"`
	InfoChannels  map[string]*InfoChannelConfig  `json:"infoChannels"`
}

type AlertChannelConfig struct {
	Name                  string            `json:"name"`
	Admins                map[string]string `json:"admins"`
	AdminGroups           map[string]string `json:"adminGroups"`
	OrderIssuesBySeverity bool              `json:"orderIssuesBySeverity"`
}

type InfoChannelConfig struct {
	Name         string `json:"name"`
	TemplatePath string `json:"templatePath"`
}
