package judge

import "testing"

func TestScore(t *testing.T) {
	tests := []struct {
		name  string
		said  string
		heard string
		want  WER
	}{
		{
			name:  "identical",
			said:  "I'd like my money back",
			heard: "I'd like my money back",
			want:  WER{Rate: 0, RefWords: 5},
		},
		{
			name:  "punctuation and case are not errors",
			said:  "Sure, it's four four eight one two.",
			heard: "sure its FOUR four eight one two",
			want:  WER{Rate: 0, RefWords: 7},
		},
		{
			// The real failure from a live Deepgram run: nova-3 dropped the
			// "one" out of the order number and the leading "It" of the next
			// sentence. The agent thanked the caller for the order number and
			// carried on.
			name:  "live run: dropped digit in an order number",
			said:  "Sure, it's four four eight one two. It was a pair of wireless headphones.",
			heard: "Sure. It's four four eight two. Was a pair of wireless headphones.",
			want: WER{
				Rate:      2.0 / 14.0,
				Deletions: 2,
				RefWords:  14,
			},
		},
		{
			name:  "substitution",
			said:  "cancel the order",
			heard: "cancel the odour",
			want:  WER{Rate: 1.0 / 3.0, Substitutions: 1, RefWords: 3},
		},
		{
			name:  "hallucinated words count as insertions",
			said:  "thanks",
			heard: "thanks very much indeed",
			want:  WER{Rate: 3.0, Insertions: 3, RefWords: 1},
		},
		{
			name:  "heard nothing at all",
			said:  "are you still there",
			heard: "",
			want:  WER{Rate: 1, Deletions: 4, RefWords: 4},
		},
		{
			name:  "said nothing but heard something",
			said:  "",
			heard: "hello",
			want:  WER{Rate: 1, Insertions: 1, RefWords: 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Score(tt.said, tt.heard)
			if !nearly(got.Rate, tt.want.Rate) {
				t.Errorf("Rate = %.4f, want %.4f", got.Rate, tt.want.Rate)
			}
			if got.Substitutions != tt.want.Substitutions {
				t.Errorf("Substitutions = %d, want %d", got.Substitutions, tt.want.Substitutions)
			}
			if got.Deletions != tt.want.Deletions {
				t.Errorf("Deletions = %d, want %d", got.Deletions, tt.want.Deletions)
			}
			if got.Insertions != tt.want.Insertions {
				t.Errorf("Insertions = %d, want %d", got.Insertions, tt.want.Insertions)
			}
			if got.RefWords != tt.want.RefWords {
				t.Errorf("RefWords = %d, want %d", got.RefWords, tt.want.RefWords)
			}
		})
	}
}

func nearly(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}
