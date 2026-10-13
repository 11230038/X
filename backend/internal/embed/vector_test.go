// The literal a Vector becomes on its way into the column. It is its own file
// because it is a statement about the type, not about the client: the database's
// vector parser is the other party to this format.
package embed

import "testing"

func TestVectorStringIsWhatTheColumnTakes(t *testing.T) {
	cases := []struct {
		vector Vector
		want   string
	}{
		{vector: Vector{1, 0, -0.5}, want: "[1,0,-0.5]"},
		{vector: Vector{0.1, 0.2}, want: "[0.1,0.2]"},
		{vector: Vector{}, want: "[]"},
		// Small magnitudes stay decimal: 'g' would have written 1e-07, which is
		// a token the database's parser is not documented to take.
		{vector: Vector{1e-7}, want: "[0.0000001]"},
		{vector: Vector{float32(1.0 / 3.0)}, want: "[0.33333334]"},
	}

	for _, testCase := range cases {
		if got := testCase.vector.String(); got != testCase.want {
			t.Errorf("Vector%v.String() = %q, want %q", []float32(testCase.vector), got, testCase.want)
		}
	}
}
