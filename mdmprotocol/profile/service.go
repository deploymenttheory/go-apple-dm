package profile

import (
	"fmt"
	"net/url"
)

// PayloadTypeProfileService is the top-level type of an OTA bootstrap profile.
const PayloadTypeProfileService = "Profile Service"

// ProfileService is the dictionary content of an OTA bootstrap profile.
// It is mutually exclusive with a configuration profile's Payloads.
type ProfileService struct {
	URL              string
	Challenge        string
	DeviceAttributes []string
}

func (s *ProfileService) validate() error {
	u, err := url.Parse(s.URL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		u.Fragment != "" {
		return fmt.Errorf(
			"%w: Profile Service requires an HTTPS URL without credentials or fragment",
			ErrInvalid,
		)
	}
	if len(s.DeviceAttributes) == 0 {
		return fmt.Errorf("%w: Profile Service requires DeviceAttributes", ErrInvalid)
	}
	for _, name := range s.DeviceAttributes {
		if name == "" {
			return fmt.Errorf("%w: empty DeviceAttributes entry", ErrInvalid)
		}
	}
	return nil
}

func (s *ProfileService) content() map[string]any {
	m := map[string]any{"URL": s.URL, "DeviceAttributes": s.DeviceAttributes}
	setIf(m, "Challenge", s.Challenge)
	return m
}

func parseService(value any) (*ProfileService, error) {
	m, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: Profile Service PayloadContent must be a dictionary", ErrParse)
	}
	s := &ProfileService{URL: str(m, "URL")}
	if v, present := m["Challenge"]; present {
		if s.Challenge, ok = v.(string); !ok {
			return nil, fmt.Errorf("%w: Profile Service Challenge must be a string", ErrParse)
		}
	}
	attrs, ok := m["DeviceAttributes"].([]any)
	if !ok {
		return nil, fmt.Errorf("%w: Profile Service DeviceAttributes must be an array", ErrParse)
	}
	for _, value := range attrs {
		name, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf(
				"%w: Profile Service DeviceAttributes must contain strings",
				ErrParse,
			)
		}
		s.DeviceAttributes = append(s.DeviceAttributes, name)
	}
	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrParse, err)
	}
	return s, nil
}
