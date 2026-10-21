package main

import "encoding/json"

// Keep missing optional data as an empty object, rather than inventing zero
// prices or loyalty counters. Successful payloads retain all fields, including 0.
func (info reservationInfo) MarshalJSON() ([]byte, error) {
	type response reservationInfo
	var payment any = info.Payment
	if info.Payment == (bookingPayment{}) {
		payment = struct{}{}
	}
	return json.Marshal(struct {
		*response
		Payment any `json:"payment"`
	}{(*response)(&info), payment})
}

func (info userInfo) MarshalJSON() ([]byte, error) {
	type response userInfo
	var loyalty any = info.Loyalty
	if info.Loyalty == (loyaltyInfo{}) {
		loyalty = struct{}{}
	}
	return json.Marshal(struct {
		*response
		Loyalty any `json:"loyalty"`
	}{(*response)(&info), loyalty})
}
