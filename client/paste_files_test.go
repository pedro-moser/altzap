package client

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseURIList(t *testing.T) {
	cases := []struct {
		name  string
		data  string
		gnome bool
		want  []string
	}{
		{
			name: "single file CRLF",
			data: "file:///home/pedro/doc.pdf\r\n",
			want: []string{"/home/pedro/doc.pdf"},
		},
		{
			name: "multiple files LF",
			data: "file:///a.txt\nfile:///b.txt\n",
			want: []string{"/a.txt", "/b.txt"},
		},
		{
			name: "percent-encoded path decodes",
			data: "file:///home/pedro/Relat%C3%B3rio%20final.pdf\r\n",
			want: []string{"/home/pedro/Relatório final.pdf"},
		},
		{
			name: "comments and blank lines skipped",
			data: "# RFC 2483 comment\r\n\r\nfile:///a.txt\r\n",
			want: []string{"/a.txt"},
		},
		{
			name: "non-file schemes ignored",
			data: "https://example.com/x\r\nfile:///a.txt\r\n",
			want: []string{"/a.txt"},
		},
		{
			name:  "gnome copy header skipped",
			data:  "copy\nfile:///a.txt\nfile:///b.txt",
			gnome: true,
			want:  []string{"/a.txt", "/b.txt"},
		},
		{
			name:  "gnome cut header skipped",
			data:  "cut\nfile:///a.txt",
			gnome: true,
			want:  []string{"/a.txt"},
		},
		{
			name: "empty payload",
			data: "",
			want: []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseURIList([]byte(tc.data), tc.gnome)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseURIList(%q) = %v, want %v", tc.data, got, tc.want)
			}
		})
	}
}

func TestPasteFilesWith(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(real, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := "file://" + real

	listOK := func() ([]string, error) {
		return []string{"text/uri-list", "text/plain"}, nil
	}

	t.Run("happy path", func(t *testing.T) {
		got, err := pasteFilesWith(listOK, func(mime string) ([]byte, error) {
			if mime != "text/uri-list" {
				t.Fatalf("asked for %q, want text/uri-list", mime)
			}
			return []byte(uri + "\r\n"), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0] != real {
			t.Errorf("got %v, want [%s]", got, real)
		}
	})

	t.Run("no uri-list offered", func(t *testing.T) {
		_, err := pasteFilesWith(func() ([]string, error) {
			return []string{"image/png", "text/plain"}, nil
		}, func(string) ([]byte, error) {
			t.Fatal("readType must not be called")
			return nil, nil
		})
		if err == nil {
			t.Error("expected error when clipboard has no file list")
		}
	})

	t.Run("gnome-copied-files fallback", func(t *testing.T) {
		got, err := pasteFilesWith(func() ([]string, error) {
			return []string{"x-special/gnome-copied-files"}, nil
		}, func(mime string) ([]byte, error) {
			if mime != "x-special/gnome-copied-files" {
				t.Fatalf("asked for %q", mime)
			}
			return []byte("copy\n" + uri), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0] != real {
			t.Errorf("got %v, want [%s]", got, real)
		}
	})

	t.Run("nonexistent files filtered out", func(t *testing.T) {
		_, err := pasteFilesWith(listOK, func(string) ([]byte, error) {
			return []byte("file:///definitely/not/there.txt\r\n"), nil
		})
		if err == nil {
			t.Error("expected error when no listed file exists")
		}
	})

	t.Run("directories filtered out", func(t *testing.T) {
		_, err := pasteFilesWith(listOK, func(string) ([]byte, error) {
			return []byte("file://" + dir + "\r\n"), nil
		})
		if err == nil {
			t.Error("expected error when the only entry is a directory")
		}
	})

	t.Run("listTypes error propagates", func(t *testing.T) {
		boom := errors.New("no wl-paste")
		_, err := pasteFilesWith(func() ([]string, error) { return nil, boom }, nil)
		if !errors.Is(err, boom) {
			t.Errorf("got %v, want %v", err, boom)
		}
	})
}

func TestPickImageMIME(t *testing.T) {
	cases := []struct {
		name  string
		types []string
		want  string
	}{
		{"png preferred", []string{"image/jpeg", "image/png"}, "image/png"},
		{"first image fallback", []string{"text/plain", "image/webp", "image/jpeg"}, "image/webp"},
		{"no image", []string{"text/plain", "text/uri-list"}, ""},
		{"empty", nil, ""},
		{"whitespace tolerated", []string{" image/png "}, "image/png"},
	}
	for _, tc := range cases {
		if got := pickImageMIME(tc.types); got != tc.want {
			t.Errorf("%s: pickImageMIME(%v) = %q, want %q", tc.name, tc.types, got, tc.want)
		}
	}
}
