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

// UnmarshalJSON retains the complete accepted-event source response.
func (p *Event) UnmarshalJSON(data []byte) error {
	type wire Event
	var v wire
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*p = Event(v)
	p.Raw = append(jsontext.Value(nil), data...)
	return nil
}

// UnmarshalJSON retains complete aggregate-status evidence.
func (p *Status) UnmarshalJSON(data []byte) error {
	type wire Status
	var v wire
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*p = Status(v)
	p.Raw = append(jsontext.Value(nil), data...)
	return nil
}

// UnmarshalJSON retains the entire original callback, independently of typed edits.
func (p *Notification) UnmarshalJSON(data []byte) error {
	type wire Notification
	var v wire
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*p = Notification(v)
	p.Raw = append(jsontext.Value(nil), data...)
	return nil
}
