package materials

import (
	"fmt"
	"path/filepath"
	"strings"
)

// contentDisposition builds the Content-Disposition header for a download.
// The RFC 5987 `filename*` value is what modern browsers use: it is
// percent-encoded UTF-8, so non-ASCII names survive. The plain `filename=`
// fallback is reduced to ASCII for clients that ignore `filename*`
// (design.md Decision 5 - putting raw UTF-8 in `filename=` made browsers
// decode the bytes as Latin-1 and show mojibake).
func contentDisposition(name string) string {
	name = filepath.Base(name)
	return fmt.Sprintf("attachment; filename=\"%s\"; filename*=UTF-8''%s",
		asciiFilename(name), rfc5987Escape(name))
}

// asciiFilename keeps only characters that are safe inside a quoted-string;
// quote, backslash, control bytes and everything non-ASCII become "_".
func asciiFilename(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c < 0x20 || c > 0x7e || c == '"' || c == '\\' {
			b.WriteByte('_')
			continue
		}
		b.WriteByte(c)
	}
	out := b.String()
	if out == "" || out == "." || out == ".." {
		return "download"
	}
	return out
}

// rfc5987Escape percent-encodes every byte outside RFC 5987's attr-char set.
func rfc5987Escape(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			strings.IndexByte("!#$&+-.^_`|~", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}
