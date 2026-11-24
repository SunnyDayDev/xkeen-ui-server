package externalaccess

import "testing"

func TestTokenIsCanonicalAndOneGenerationIsAdmitted(t *testing.T) {
	token, digest, err := Issue()
	if err != nil {
		t.Fatal(err)
	}
	state := Settings{Enabled: true, Protected: true, Version: 3, HasToken: true}
	for _, tc := range []struct {
		headers []string
		ok      bool
	}{{nil, false}, {[]string{"Bearer " + token}, true}, {[]string{"Bearer " + token + "="}, false}, {[]string{"Bearer " + token, "Bearer " + token}, false}} {
		if _, err := Admit(state, digest[:], tc.headers); (err == nil) != tc.ok {
			t.Errorf("unexpected admission")
		}
	}
	state.Protected = false
	if _, err := Admit(state, nil, nil); err != nil {
		t.Fatal(err)
	}
	state.Enabled = false
	if _, err := Admit(state, nil, nil); err != ErrDisabled {
		t.Fatal(err)
	}
}
