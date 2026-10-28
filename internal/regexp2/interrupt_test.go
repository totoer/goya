package regexp2

import "testing"

func TestInterruptBacktracking(t *testing.T) {
	for _, next := range []bool{false, true} {
		t.Run(map[bool]string{false: "initial", true: "next"}[next], func(t *testing.T) {
			re := MustCompile(`z|(?<=z)(a+)+(?=b)$`)
			input := []rune("zaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			var match *Match
			var err error
			if next {
				match, err = re.FindRunesMatchStartingAt(input, 0)
				if err != nil || match == nil || match.String() != "z" {
					t.Fatalf("first match: %v %v", match, err)
				}
			}
			stop := new(int)
			polls := 0
			check := func() {
				polls++
				if polls == 10 {
					panic(stop)
				}
			}
			func() {
				defer func() {
					if p := recover(); p != stop {
						t.Fatalf("interruption: %v (polls %d)", p, polls)
					}
				}()
				if next {
					_, err = re.FindNextMatchWithInterrupt(match, check)
				} else {
					_, err = re.FindRunesMatchStartingAtWithInterrupt(input, 1, check)
				}
				t.Fatalf("backtracking returned before interruption: %v", err)
			}()
			// Reusing the pooled runner must not invoke the old hook.
			match, err = re.FindRunesMatchStartingAt([]rune("zaab"), 0)
			if err != nil || match == nil || match.String() != "z" || polls != 10 {
				t.Fatalf("reuse after interruption: %v %v (polls %d)", match, err, polls)
			}
		})
	}
}

func TestInterruptHookIsPerCall(t *testing.T) {
	re := MustCompile(`^(a+)+(?=b)$`)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan any, 1)
	stop := new(int)
	go func() {
		defer func() { done <- recover() }()
		_, _ = re.FindRunesMatchStartingAtWithInterrupt([]rune("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), 0, func() {
			close(entered)
			<-release
			panic(stop)
		})
	}()
	<-entered
	// The same compiled expression can execute in another runtime while the
	// first runner is suspended inside its interruption hook.
	match, err := re.FindRunesMatchStartingAt([]rune("b"), 0)
	close(release)
	if p := <-done; p != stop {
		t.Fatalf("interruption: %v", p)
	}
	if err != nil || match != nil {
		t.Fatalf("independent match: %v %v", match, err)
	}
}
