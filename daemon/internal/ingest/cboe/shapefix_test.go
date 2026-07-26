package cboe

import "testing"

// Cboe changed "SUM OF ALL PRODUCTS" from a single object to an ARRAY carrying
// both VOLUME and OPEN INTEREST. The array form was a hard parse failure and had
// left the cboe_pc table at zero rows.
func TestParseDailyAcceptsArrayShape(t *testing.T) {
	raw := []byte(`{
	  "ratios":[{"name":"TOTAL PUT/CALL RATIO","value":"1.01"}],
	  "SUM OF ALL PRODUCTS":[
	    {"name":"VOLUME","call":6031257,"put":6071481,"total":12102738},
	    {"name":"OPEN INTEREST","call":339998261,"put":255605343,"total":595603604}
	  ]}`)
	s, err := ParseDaily(raw, "2026-07-22")
	if err != nil {
		t.Fatalf("array shape must parse: %v", err)
	}
	if s.TotalVol != 12102738 {
		t.Fatalf("TotalVol = %v, want the VOLUME record 12102738", s.TotalVol)
	}
	if s.TotalPC != 1.01 {
		t.Fatalf("TotalPC = %v, want 1.01", s.TotalPC)
	}
}

// The load-bearing guarantee: the record is chosen BY NAME, never by position.
// OPEN INTEREST runs ~50x larger than volume, so taking index 0 would silently
// poison the put/call VOLUME — a model feature — the day Cboe reorders.
func TestVolumeSelectedByNameNotPosition(t *testing.T) {
	raw := []byte(`{
	  "ratios":[{"name":"TOTAL PUT/CALL RATIO","value":"1.01"}],
	  "SUM OF ALL PRODUCTS":[
	    {"name":"OPEN INTEREST","call":339998261,"put":255605343,"total":595603604},
	    {"name":"VOLUME","call":6031257,"put":6071481,"total":12102738}
	  ]}`)
	s, err := ParseDaily(raw, "2026-07-22")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.TotalVol != 12102738 {
		t.Fatalf("TotalVol = %v — picked by POSITION, not name; open interest would poison pc_total", s.TotalVol)
	}
}

// The legacy object shape must keep parsing so archived documents still work.
func TestParseDailyStillAcceptsObjectShape(t *testing.T) {
	raw := []byte(`{
	  "ratios":[{"name":"TOTAL PUT/CALL RATIO","value":"0.9"}],
	  "SUM OF ALL PRODUCTS":{"name":"VOLUME","call":100,"put":200,"total":300}}`)
	s, err := ParseDaily(raw, "2026-07-22")
	if err != nil {
		t.Fatalf("legacy object shape must still parse: %v", err)
	}
	if s.TotalVol != 300 || s.PutVol != 200 {
		t.Fatalf("legacy shape mis-parsed: %+v", s)
	}
}

// A shape we understand but which carries no VOLUME record yields absence, not
// zeros presented as data. The ratio is still the headline and still returned.
func TestMissingVolumeRecordYieldsAbsenceNotZeros(t *testing.T) {
	raw := []byte(`{
	  "ratios":[{"name":"TOTAL PUT/CALL RATIO","value":"1.2"}],
	  "SUM OF ALL PRODUCTS":[{"name":"OPEN INTEREST","call":1,"put":2,"total":3}]}`)
	s, err := ParseDaily(raw, "2026-07-22")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.TotalVol != 0 || s.CallVol != 0 {
		t.Fatalf("absent VOLUME must leave volumes zero-valued, got %+v", s)
	}
	if s.TotalPC != 1.2 {
		t.Fatalf("the ratio must still be read: %v", s.TotalPC)
	}
}
