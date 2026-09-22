package axm

import (
	"encoding/json"
	"reflect"
)

// marshalExtra preserves unknown attributes while reserving known field names for
// their typed values. Original null and omission information remains in resource Raw.
func marshalExtra(value any, extra Extra) ([]byte, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return nil, err
	}
	known := jsonKeys(reflect.TypeOf(value))
	for key, value := range extra {
		if _, reserved := known[key]; !reserved {
			fields[key] = value
		}
	}
	return json.Marshal(fields)
}

// UnmarshalJSON retains the original organization-device resource.
func (r *OrgDevice) UnmarshalJSON(b []byte) error {
	type plain OrgDevice
	var v plain
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*r = OrgDevice(v)
	r.Raw = append(json.RawMessage(nil), b...)
	return nil
}

// UnmarshalJSON retains the original coverage resource.
func (r *AppleCareCoverage) UnmarshalJSON(b []byte) error {
	type plain AppleCareCoverage
	var v plain
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*r = AppleCareCoverage(v)
	r.Raw = append(json.RawMessage(nil), b...)
	return nil
}

// UnmarshalJSON retains the original Apple-managed device resource.
func (r *MDMDevice) UnmarshalJSON(b []byte) error {
	type plain MDMDevice
	var v plain
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*r = MDMDevice(v)
	r.Raw = append(json.RawMessage(nil), b...)
	return nil
}

// UnmarshalJSON retains the original Apple-managed device detail resource.
func (r *MDMDeviceDetail) UnmarshalJSON(b []byte) error {
	type plain MDMDeviceDetail
	var v plain
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*r = MDMDeviceDetail(v)
	r.Raw = append(json.RawMessage(nil), b...)
	return nil
}

// UnmarshalJSON retains the original device-management service resource.
func (r *MDMServer) UnmarshalJSON(b []byte) error {
	type plain MDMServer
	var v plain
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*r = MDMServer(v)
	r.Raw = append(json.RawMessage(nil), b...)
	return nil
}

// MarshalJSON includes forward-compatible organization-device attributes.
func (a OrgDeviceAttributes) MarshalJSON() ([]byte, error) {
	type plain OrgDeviceAttributes
	return marshalExtra(plain(a), a.Extra)
}

// MarshalJSON includes forward-compatible coverage attributes.
func (a AppleCareCoverageAttributes) MarshalJSON() ([]byte, error) {
	type plain AppleCareCoverageAttributes
	return marshalExtra(plain(a), a.Extra)
}

// MarshalJSON includes forward-compatible Apple-managed inventory attributes.
func (a MDMDeviceDetailAttributes) MarshalJSON() ([]byte, error) {
	type plain MDMDeviceDetailAttributes
	return marshalExtra(plain(a), a.Extra)
}

// MarshalJSON includes forward-compatible device-management service attributes.
func (a MDMServerAttributes) MarshalJSON() ([]byte, error) {
	type plain MDMServerAttributes
	return marshalExtra(plain(a), a.Extra)
}
