package edgeos

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/britannic/blacklist/internal/regx"
)

// source struct for normalizing EdgeOS data.
type source struct {
	*Env
	Objects
	desc     string
	disabled bool
	err      error
	exc      []string
	file     string
	inc      []string
	ip       string
	iface    IFace
	ltype    string
	nType    ntype
	name     string
	prefix   string
	r        io.Reader
	url      string
}

func (s *source) addSource(srcName [][]byte, n string) {
	if bytes.Equal(srcName[1], []byte(src)) {
		s.name = string(srcName[2])
		s.nType = getType(n).(ntype)
	}
}

func (s *source) area() string {
	switch getType(s.nType).(string) {
	case domains, PreDomns:
		return domains
	case rootNode:
		return roots
	}
	return hosts
}

// excludes returns an io.Reader of blacklist includes
func (s *source) excludes() io.Reader {
	sort.Strings(s.exc)
	return strings.NewReader(strings.Join(s.exc, "\n"))
}

func (s *source) filename(area string) string {
	switch s.nType {
	case excRoot, preRoot:
		return fmt.Sprintf(s.FnFmt, s.Dir, roots, s.name, s.Ext)
	case excDomn, preDomn:
		return fmt.Sprintf(s.FnFmt, s.Dir, domains, s.name, s.Ext)
	case excHost, preHost:
		return fmt.Sprintf(s.FnFmt, s.Dir, hosts, s.name, s.Ext)
	}
	return fmt.Sprintf(s.FnFmt, s.Dir, area, s.name, s.Ext)
}

// includes returns an io.Reader of blacklist includes
func (s *source) includes() io.Reader {
	sort.Strings(s.inc)
	return strings.NewReader(strings.Join(s.inc, "\n"))
}

func isntSource(nx []string) bool {
	if len(nx) < 1 {
		return true
	}
	return nx[len(nx)-1] != src
}

func newSource() *source {
	return &source{
		Objects: Objects{},
		exc:     []string{},
		inc:     []string{},
	}
}

func (s *source) setFilePrefix(format string) string {
	switch s.nType {
	case excDomn, preDomn:
		return fmt.Sprintf(format, domains, s.name)
	case excHost, preHost:
		return fmt.Sprintf(format, hosts, s.name)
	case excRoot, preRoot:
		return fmt.Sprintf(format, roots, s.name)
	}
	return fmt.Sprintf(format, getType(s.nType), s.name)
}

func printArray(a []string) (s string) {
	if len(a) < 1 {
		s = fmt.Sprintf("              %q\n", "**No entries found**")
		return s
	}
	for _, e := range a {
		s = fmt.Sprintf("%s              %q\n", s, e)
	}
	return s
}

func pad(s string) string {
	return fmt.Sprintf("%s %-*s", s, 13-len(s), " ")
}

// Process extracts hosts/domains from downloaded raw content
func (s *source) process() *bList {
	var (
		area                     = typeInt(s.nType)
		b                        = bufio.NewScanner(s.r)
		dropped, extracted, kept int
		find                     = regx.NewRegex()
		l                        = list{RWMutex: &sync.RWMutex{}, entry: make(entry)}
		ok                       bool
	)

	for b.Scan() {
		line := bytes.ToLower(bytes.TrimSpace(b.Bytes()))

		switch {
		case bytes.HasPrefix(line, []byte("#")), bytes.HasPrefix(line, []byte("//")), bytes.HasPrefix(line, []byte("<")):
			continue
		case bytes.HasPrefix(line, []byte(s.prefix)):
			if line, ok = find.StripPrefixAndSuffix(line, s.prefix); ok {
				for _, fqdn := range find.RX[regx.FQDN].FindAll(line, -1) {
					extracted++
					if s.Dex.subKeyExists(fqdn) {
						dropped++
						continue
					}
					if !s.Exc.keyExists(fqdn) {
						kept++
						s.Exc.set(fqdn)
						l.set(fqdn)
						continue
					}
					dropped++
				}
			}
		}
	}

	switch s.nType {
	case domn, excDomn, excRoot:
		s.Dex.merge(&l)
	}

	s.sum(area, dropped, extracted, kept)

	return &bList{
		file: s.filename(area),
		r:    formatData(getDnsmasqPrefix(s), &l),
		size: kept,
	}
}

// Stringer for *source
func (s *source) String() string {
	a := func(s string) string {
		if s == "" {
			return "**Undefined**"
		}
		return s
	}

	return strings.Join(
		[]string{
			"\n",
			fmt.Sprintf("%s%q\n", pad("Desc:"), a(s.desc)),
			fmt.Sprintf("%s%q\n", pad("Disabled:"), booltoStr(s.disabled)),
			fmt.Sprintf("%s%q\n", pad("File:"), a(s.file)),
			fmt.Sprintf("%s%q\n", pad("IP:"), a(s.ip)),
			fmt.Sprintf("%s%q\n", pad("Ltype:"), a(s.ltype)),
			fmt.Sprintf("%s%q\n", pad("Name:"), a(s.name)),
			fmt.Sprintf("%s%q\n", pad("nType:"), s.nType),
			fmt.Sprintf("%s%q\n", pad("Prefix:"), a(s.prefix)),
			fmt.Sprintf("%s%q\n", pad("Type:"), getType(s.nType)),
			fmt.Sprintf("%s%q\n", pad("URL:"), a(s.url)),
			fmt.Sprintf("Whitelist:\n%s", printArray(s.exc)),
			fmt.Sprintf("Blacklist:\n%s", printArray(s.inc)),
		},
		"",
	)
}

func (s *source) sum(area string, dropped, extracted, kept int) {
	// Let's do some accounting
	ctr := s.ctr.stat
	atomic.AddInt32(&ctr[area].dropped, int32(dropped))
	atomic.AddInt32(&ctr[area].extracted, int32(extracted))
	atomic.AddInt32(&ctr[area].kept, int32(kept))

	switch {
	case kept > 0:
		s.Log.Infof("%s: downloaded: %d", s.name, extracted)
		s.Log.Infof("%s: extracted: %d", s.name, kept)
		s.Log.Infof("%s: dropped: %d", s.name, dropped)
	case extracted > 0 && dropped == extracted:
		s.Log.Warningf("%s: 0 records processed - check source and/or configuration", s.name)
	}
}
