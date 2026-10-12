package main

import "testing"

func TestLocalBaseTurnsAListenAddressIntoOneToDial(t *testing.T) {
	tests := []struct{ name, addr, want string }{
		{name: "every interface is not an address to dial", addr: ":8080", want: "http://127.0.0.1:8080"},
		{name: "a named host is kept", addr: "127.0.0.1:9000", want: "http://127.0.0.1:9000"},
		{name: "an address with no port is kept as it is", addr: "localhost", want: "http://localhost"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := localBase(test.addr); got != test.want {
				t.Errorf("localBase(%q) = %q, want %q", test.addr, got, test.want)
			}
		})
	}
}

func TestSocketURLIsTheSameServerOverTheSocket(t *testing.T) {
	tests := []struct {
		name string
		base string
		want string
	}{
		{name: "http becomes ws", base: "http://127.0.0.1:8080", want: "ws://127.0.0.1:8080/api/v1/ws"},
		{name: "https becomes wss", base: "https://example.test", want: "wss://example.test/api/v1/ws"},
		{name: "a trailing slash does not double", base: "http://example.test/", want: "ws://example.test/api/v1/ws"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe := &probe{options: options{base: test.base}}
			if got := probe.socketURL(); got != test.want {
				t.Errorf("socketURL() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestTheProtocolVersionIsTheOneAskedFor(t *testing.T) {
	tests := []struct {
		name    string
		options options
		want    string
	}{
		{name: "unasked for is the version this probe speaks", want: defaultProtocolVersion},
		{name: "another one is spoken as given", options: options{protocolVersion: "3.0"}, want: "3.0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := (&probe{options: test.options}).version(); got != test.want {
				t.Errorf("version() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestFlagsRefuseRunsThatCannotWork(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantFail bool
	}{
		{name: "a token is enough", args: []string{"-token", "t"}},
		{name: "credentials are enough", args: []string{"-username", "u", "-password", "p"}},
		{name: "neither is not", args: nil, wantFail: true},
		{name: "a negative count is not", args: []string{"-token", "t", "-drop-after", "-1"}, wantFail: true},
		{
			name:     "two different probes at once is not",
			args:     []string{"-token", "t", "-cancel-after", "3", "-drop-after", "3"},
			wantFail: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseFlags(test.args)
			if test.wantFail && err == nil {
				t.Error("parseFlags() accepted a run that cannot work")
			}
			if !test.wantFail && err != nil {
				t.Errorf("parseFlags() error = %v", err)
			}
		})
	}
}
