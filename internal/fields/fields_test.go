package fields

import "testing"

func TestDefsAndValues(t *testing.T) {
	bad := []Defs{
		{Fields: []Def{{Key: "Talla", Label: "Talla", Type: "text"}}},
		{Fields: []Def{{Key: "a", Label: "A", Type: "text"}, {Key: "a", Label: "B", Type: "text"}}},
		{Fields: []Def{{Key: "a", Label: "", Type: "text"}}},
		{Fields: []Def{{Key: "a", Label: "A", Type: "raro"}}},
		{Fields: []Def{{Key: "a", Label: "A", Type: "select"}}},
	}
	for i, d := range bad {
		if d.Clean() == nil {
			t.Errorf("bad definition %d accepted", i)
		}
	}
	d := Defs{Fields: []Def{
		{Key: "talla", Label: "Talla", Type: "select", Options: []string{"CH", "M", "G"}},
		{Key: "puntos", Label: "Puntos", Type: "number"},
		{Key: "nacimiento", Label: "Nacimiento", Type: "date", Personal: true},
		{Key: "rfc", Label: "RFC", Type: "text", Required: true, Personal: true},
	}}
	if err := d.Clean(); err != nil {
		t.Fatal(err)
	}
	plain, personal, err := d.Split(map[string]string{"talla": "M", "puntos": "12.5", "nacimiento": "1990-05-01", "rfc": "XAXX010101000"})
	if err != nil || plain["talla"] != "M" || personal["rfc"] == "" || plain["rfc"] != "" {
		t.Fatalf("split: %v %v %v", plain, personal, err)
	}
	for _, v := range []map[string]string{
		{"rfc": "x", "talla": "XL"},
		{"rfc": "x", "puntos": "doce"},
		{"rfc": "x", "nacimiento": "01/05/1990"},
		{"talla": "M"},            // required missing
		{"rfc": "x", "otro": "y"}, // unknown key
	} {
		if _, _, err := d.Split(v); err == nil {
			t.Errorf("bad values %v accepted", v)
		}
	}
}
