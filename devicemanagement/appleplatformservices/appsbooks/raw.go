package appsbooks

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
)

// UnmarshalJSON retains the complete assignment page, including unmodeled fields.
func (p *AssignmentsPage) UnmarshalJSON(data []byte) error {
	type wire AssignmentsPage
	var v wire
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*p = AssignmentsPage(v)
	p.Raw = append(jsontext.Value(nil), data...)
	return nil
}

// UnmarshalJSON retains the complete user page, including unmodeled fields.
func (p *UsersPage) UnmarshalJSON(data []byte) error {
	type wire UsersPage
	var v wire
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*p = UsersPage(v)
	p.Raw = append(jsontext.Value(nil), data...)
	return nil
}

// UnmarshalJSON retains the complete configuration. Raw may contain notification
// credentials and requires the same protection as the typed configuration.
func (p *ClientConfiguration) UnmarshalJSON(data []byte) error {
	type wire ClientConfiguration
	var v wire
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*p = ClientConfiguration(v)
	p.Raw = append(jsontext.Value(nil), data...)
	return nil
}
