package corpus

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/mail"
	"os"
	"path"
	"strings"
	"unicode/utf8"
)

// maxMessageSize is the largest message taken from the mail archive.
//
// It is smaller than MaxDocumentSize because a megabyte of email is a base64
// attachment that somebody pasted inline, and there are enough of those to move
// a total that is meant to describe half a million messages.
const maxMessageSize = 256 << 10

// buildEnron turns the mail archive into one document per message.
//
// The archive is a maildir: every message is a file whose path is the account
// it belongs to, the folder the person filed it in, and a number. All three
// carry information. The account is the closest thing this corpus has to a
// tenant, the folder is a real taxonomy that a real person maintained by hand,
// and the number is the order the messages were collected in.
//
// Duplicates are kept. The same message appears in a dozen mailboxes with
// different headers, and that is not a flaw in the corpus, it is the single
// most useful thing about it: an engine that does not have to deal with near
// duplicate documents has not been measured on a real mailbox.
func buildEnron(archive string, enc *json.Encoder, limit int, _ string) (Stats, error) {
	var st Stats

	f, err := os.Open(archive)
	if err != nil {
		return st, err
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(bufio.NewReaderSize(f, 1<<20))
	if err != nil {
		return st, err
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	// One buffer for the whole walk. There are half a million entries and an
	// allocation each would be most of the time this spends.
	var buf bytes.Buffer
	buf.Grow(maxMessageSize)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return st, nil
		}
		if err != nil {
			return st, err
		}
		if h.Typeflag != tar.TypeReg || h.Size > maxMessageSize {
			continue
		}

		rel := strings.TrimPrefix(path.Clean(h.Name), "maildir/")
		account, folder, ok := strings.Cut(rel, "/")
		if !ok || account == "" {
			continue
		}

		buf.Reset()
		if _, err := io.Copy(&buf, tr); err != nil {
			return st, err
		}
		raw := buf.Bytes()

		// Some of these are not valid UTF-8, because a mail client in 2001 sent
		// whatever encoding it felt like and nothing rewrote it since. They are
		// left out rather than transcoded, because guessing an encoding is a
		// decision that would land differently on each engine and none of the
		// engines here would be the thing being measured.
		if !utf8.Valid(raw) {
			continue
		}

		subject, text := split(raw)
		if len(text) == 0 {
			continue
		}

		doc := Document{
			ID:        rel,
			Repo:      account,
			Path:      folder,
			Title:     subject,
			Body:      text,
			Extension: "eml",
		}
		if err := enc.Encode(doc); err != nil {
			return st, err
		}
		st.Documents++
		st.Bytes += int64(len(text))
		if limit > 0 && st.Documents >= limit {
			return st, nil
		}
	}
}

// split pulls the subject and the message body out of one raw message.
//
// The mail parser is tried first and a hand split at the first blank line is
// the fallback, because a real mailbox contains messages the parser rejects:
// headers that continue onto a line starting with a letter, addresses with an
// unbalanced quote in them, and a handful of files that are a body with no
// headers at all. Failing on those would drop several thousand real messages
// and would make this corpus a corpus of well formed mail, which is exactly the
// thing that would stop it being useful.
func split(raw []byte) (subject, text string) {
	if m, err := mail.ReadMessage(bytes.NewReader(raw)); err == nil {
		rest, err := io.ReadAll(m.Body)
		if err == nil {
			return m.Header.Get("Subject"), strings.TrimSpace(string(rest))
		}
	}

	head, rest, ok := bytes.Cut(raw, []byte("\n\n"))
	if !ok {
		return "", strings.TrimSpace(string(raw))
	}
	for line := range strings.Lines(string(head)) {
		if after, found := strings.CutPrefix(line, "Subject:"); found {
			subject = strings.TrimSpace(after)
			break
		}
	}
	return subject, strings.TrimSpace(string(rest))
}
