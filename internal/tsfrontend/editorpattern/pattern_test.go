// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the Apache License, Version 2.0. See ../LICENSE.typescript.
// Adapted from the pinned TypeScript 6.0.3 patternMatcher unit cases.
package editorpattern

import (
	"reflect"
	"strings"
	"testing"
)

func TestPinnedPatternMatches(t *testing.T) {
	for _, tc := range []struct {
		candidate, pattern, container string
		want                          *Match
	}{
		{"Foo", "Foo", "", &Match{0, true}},
		{"foo", "Foo", "", &Match{0, false}},
		{"Foo", "Fo", "", &Match{1, true}},
		{"Foo", "fo", "", &Match{1, false}},
		{"FogBar", "FB", "", &Match{3, true}},
		{"FogBar", "FoB", "", &Match{3, true}},
		{"FogBar", "FBB", "", nil},
		{"FogBar", "FoooB", "", nil},
		{"FogBarBaz", "FZ", "", nil},
		{"FogBarBaz", "ZZ", "", nil},
		{"SimpleUIElement", "SiUI", "", &Match{3, true}},
		{"FogBar", "b", "", &Match{2, false}},
		{"FogBar", "fB", "", &Match{3, false}},
		{"_fogBar", "_fB", "", &Match{3, true}},
		{"_fogBar", "fB", "", &Match{3, true}},
		{"_FogBar", "_fB", "", &Match{3, false}},
		{"Fog_Bar", "FB", "", &Match{3, true}},
		{"Fog_Bar", "F_B", "", &Match{3, true}},
		{"Fog_Bar", "F__B", "", nil},
		{"Fog_Bar", "f_B", "", &Match{3, false}},
		{"Fog_Bar", "F_b", "", &Match{3, false}},
		{"FogBarChangedEventArgs", "changedeventargs", "", &Match{2, false}},
		{"FogBarChangedEventArgs", "changedeventarrrgh", "", nil},
		{"ABCDEFGH", "bcd", "", &Match{2, false}},
		{"AbcdefghijEfgHij", "efghij", "", nil},
		{"AddMetadataReference", "addmetadatareference", "", &Match{0, false}},
		{"AddMetadataReference", "add", "", &Match{1, false}},
		{"AddMetadataReference", "metadata", "", &Match{2, false}},
		{"AddMetadataReference", "Add", "", &Match{1, true}},
		{"AddMetadataReference", "Metadata", "", &Match{2, true}},
		{"AddMetadataReference", "A", "", &Match{1, true}},
		{"AddMetadataReference", "M", "", &Match{2, true}},
		{"AddMetadataReference", "add metadata", "", &Match{1, false}},
		{"AddMetadataReference", "A M", "", &Match{1, true}},
		{"AddMetadataReference", "AM", "", &Match{3, true}},
		{"AddMetadataReference", "ref Metadata", "", &Match{2, true}},
		{"AddMetadataReference", "ref M", "", &Match{2, true}},
		{"AddMetadataReference", "AMRe", "", &Match{3, true}},
		{"AddMetadataReference", "add Meta", "", &Match{1, false}},
		{"AddMetadataReference", "Add meta", "", &Match{1, true}},
		{"AddMetadataReference", "Add Meta", "", &Match{1, true}},
		{"AddMetadataReference", "mEta", "", nil},
		{"AddMetadataReference", "Data", "", nil},
		{"GetKeyWord", "K*W", "", &Match{2, true}},
		{"Operator", "a", "", nil},
		{"FooAttribute", "a", "", &Match{2, false}},
		{"Quux", "B.Q", "Foo.Bar.Baz", &Match{1, true}},
		{"Quux", "C.Q", "Foo.Bar.Baz", nil},
		{"Quux", "B.B.Q", "Foo.Bar.Baz", &Match{1, true}},
		{"Quux", "Baz.Quux", "Foo.Bar.Baz", &Match{0, true}},
		{"Quux", "F.B.B.Quux", "Foo.Bar.Baz", &Match{1, true}},
		{"Quux", "F.F.B.B.Quux", "Foo.Bar.Baz", nil},
		{"UIElement", "UIElement", "", &Match{0, true}},
		{"GetKeyword", "UIElement", "", nil},
	} {
		var containers []string
		if tc.container != "" {
			containers = strings.Split(tc.container, ".")
		}
		got := New(tc.pattern).Match(tc.candidate, containers)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q / %q in %q: got %+v want %+v", tc.candidate, tc.pattern, tc.container, got, tc.want)
		}
	}
}
