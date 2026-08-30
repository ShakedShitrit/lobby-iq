package rlstats

import (
	"fmt"
	"net/url"
	"strings"
)

var platformDisplayNames = map[string]string{
	"Steam":   "Steam",
	"Epic":    "Epic Games",
	"PsyNet":  "PlayStation",
	"Switch":  "Nintendo Switch",
	"XboxOne": "Xbox",
}

func slugNameGetter(id string, name string) string {
	return strings.TrimSpace(name)
}

func slugIdGetter(id string, name string) string {
	return id
}

var slugIdGetters = map[string]func(id string, name string) string{
	"steam":  slugIdGetter,
	"epic":   slugNameGetter,
	"psn":    slugNameGetter,
	"switch": slugNameGetter,
	"xbl":    slugNameGetter,
}

var trackerPlatformSlugs = map[string]string{
	"steam":   "steam",
	"epic":    "epic",
	"psynet":  "psn",
	"ps4":     "psn",
	"ps5":     "psn",
	"switch":  "switch",
	"xboxone": "xbl",
}

// PrimaryId is "<Platform>|<PlatformID>|<Instance>", e.g.
// "Epic|1add6955b6de40d3a569169e1a0b740b|0".
func (p Player) platformAndID() (platform, id string) {
	parts := strings.SplitN(p.PrimaryId, "|", 3)
	if len(parts) > 0 {
		platform = parts[0]
	}
	if len(parts) > 1 {
		id = parts[1]
	}
	return platform, id
}

// Platform returns a human-friendly platform name, e.g. "Epic Games".
func (p Player) Platform() string {
	platform, _ := p.platformAndID()
	if platform == "" {
		return "Unknown"
	}
	if name, ok := platformDisplayNames[platform]; ok {
		return name
	}
	return platform
}

// TrackerURL returns the rocketleague.tracker.gg profile URL for this
// player.
func (p Player) TrackerURL() string {
	platform, id := p.platformAndID()

	slug, ok := trackerPlatformSlugs[strings.ToLower(platform)]
	if !ok {
		slug = strings.ToLower(platform)
	}

	// Epic, PlayStation, and Xbox don't resolve tracker.gg profiles by
	// account ID, only by display name/username.
	identifier := id
	getter, ok := slugIdGetters[slug]
	if ok {
		identifier = getter(id, p.Name)
	}

	return fmt.Sprintf("https://rocketleague.tracker.network/rocket-league/profile/%s/%s/overview", slug, url.PathEscape(identifier))
}
