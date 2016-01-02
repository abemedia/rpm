package rpm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"time"
)

// A PackageFile is an RPM package loaded from a stored filed.
type PackageFile struct {
	Lead    Lead
	Headers Headers
}

// A Lead is the deprecated lead section of an RPM file which is used in legacy
// rpm versions to store package metadata.
type Lead struct {
	VersionMajor    int
	VersionMinor    int
	Name            string
	Type            int
	Architecture    int
	OperatingSystem int
	SignatureType   int
}

// A Header stores metadata about a rpm package.
type Header struct {
	Version    int
	IndexCount int
	Length     int64
	Indexes    IndexEntries
}

// Headers is an array of Header structs.
type Headers []Header

// OpenPackageFile reads a rpm package from the file systems and returns a pointer
// to it.
func OpenPackageFile(path string) (*PackageFile, error) {
	// open file
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("Error opening RPM file: %s", err)
	}
	defer f.Close()

	return ReadPackageFile(f)
}

// ReadPackageFile reads a rpm package from a stream and returns a pointer to it.
func ReadPackageFile(r io.Reader) (*PackageFile, error) {
	p := &PackageFile{}

	// read the deprecated "lead"
	lead := make([]byte, 96)
	n, err := r.Read(lead)
	if err != nil {
		return nil, fmt.Errorf("Error reading RPM Lead section: %s", err)
	}

	if n != 96 {
		return nil, fmt.Errorf("RPM Lead section is incorrect length")
	}

	// check magic number
	if 0 != bytes.Compare(lead[:4], []byte{0xED, 0xAB, 0xEE, 0xDB}) {
		return nil, fmt.Errorf("RPM file descriptor is invalid")
	}

	// translate lead
	p.Lead.VersionMajor = int(lead[5])
	p.Lead.VersionMinor = int(lead[6])
	p.Lead.Type = (int(lead[7]) << 8) + int(lead[8])
	p.Lead.Architecture = (int(lead[9]) << 8) + int(lead[10])
	p.Lead.Name = string(lead[10:77])
	p.Lead.OperatingSystem = (int(lead[76]) << 8) + int(lead[77])
	p.Lead.SignatureType = (int(lead[78]) << 8) + int(lead[79])

	// TODO: validate lead value ranges

	// parse headers
	p.Headers = make(Headers, 0)

	// TODO: find last header without using hard limit of 2
	for i := 1; i < 3; i++ {
		// read the "header structure header"
		header := make([]byte, 16)
		n, err = r.Read(header)
		if err != nil {
			return nil, fmt.Errorf("Error reading RPM structure header for header %d: %v", i, err)
		}

		if n != 16 {
			return nil, fmt.Errorf("Error reading RPM structure header for header %d: only %d bytes returned", i, n)
		}

		// check magic number
		if 0 != bytes.Compare(header[:3], []byte{0x8E, 0xAD, 0xE8}) {
			return nil, fmt.Errorf("RPM header %d is invalid", i)
		}

		// translate header
		h := Header{}
		h.Version = int(header[3])
		h.IndexCount = (int(header[8]) << 24) + (int(header[9]) << 16) + (int(header[10]) << 8) + int(header[11])
		h.Length = (int64(header[12]) << 24) + (int64(header[13]) << 16) + (int64(header[14]) << 8) + int64(header[15])
		h.Indexes = make(IndexEntries, h.IndexCount)

		// read indexes
		indexLength := 16 * h.IndexCount
		indexes := make([]byte, indexLength)
		n, err = r.Read(indexes)
		if err != nil {
			return nil, fmt.Errorf("Error reading index entries for header %d: %v", i, err)
		}

		if n != indexLength {
			return nil, fmt.Errorf("Error reading index entries for header %d: only %d bytes returned", i, n)
		}

		for x := 0; x < h.IndexCount; x++ {
			o := 16 * x
			index := IndexEntry{}

			index.Tag = (int64(indexes[o]) << 24) + (int64(indexes[o+1]) << 16) + (int64(indexes[o+2]) << 8) + int64(indexes[o+3])
			index.Type = (int64(indexes[o+4]) << 24) + (int64(indexes[o+5]) << 16) + (int64(indexes[o+6]) << 8) + int64(indexes[o+7])
			index.Offset = (int64(indexes[o+8]) << 24) + (int64(indexes[o+9]) << 16) + (int64(indexes[o+10]) << 8) + int64(indexes[o+11])
			index.ItemCount = (int64(indexes[o+12]) << 24) + (int64(indexes[o+13]) << 16) + (int64(indexes[o+14]) << 8) + int64(indexes[o+15])
			h.Indexes[x] = index
		}

		// read the "store"
		store := make([]byte, h.Length)
		n, err = r.Read(store)
		if err != nil {
			return nil, fmt.Errorf("Error reading store for header %d: %v", i, err)
		}

		if int64(n) != h.Length {
			return nil, fmt.Errorf("Error reading store for header %d: only %d bytes returned", i, n)
		}

		for x := 0; x < h.IndexCount; x++ {
			index := h.Indexes[x]
			o := index.Offset

			switch index.Type {
			case IndexDataTypeChar:
				vals := make([]uint8, index.ItemCount)
				for v := int64(0); v < index.ItemCount; v++ {
					vals[v] = uint8(store[o])
					o += 1
				}

				index.Value = vals

			case IndexDataTypeInt8:
				vals := make([]int8, index.ItemCount)
				for v := int64(0); v < index.ItemCount; v++ {
					vals[v] = int8(store[o])
					o += 1
				}

				index.Value = vals

			case IndexDataTypeInt16:
				vals := make([]int16, index.ItemCount)
				for v := int64(0); v < index.ItemCount; v++ {
					vals[v] = int16(binary.BigEndian.Uint16(store[o : o+2]))
					o += 2
				}

				index.Value = vals

			case IndexDataTypeInt32:
				vals := make([]int32, index.ItemCount)
				for v := int64(0); v < index.ItemCount; v++ {
					vals[v] = int32(binary.BigEndian.Uint32(store[o : o+4]))
					o += 4
				}

				index.Value = vals

			case IndexDataTypeInt64:
				vals := make([]int64, index.ItemCount)
				for v := int64(0); v < index.ItemCount; v++ {
					vals[v] = int64(binary.BigEndian.Uint64(store[o : o+8]))
					o += 8
				}

				index.Value = vals

			case IndexDataTypeBinary:
				b := make([]byte, index.ItemCount)
				copy(b, store[o:o+index.ItemCount])

				index.Value = b

			case IndexDataTypeString, IndexDataTypeStringArray, IndexDataTypeI8NString:
				vals := make([]string, index.ItemCount)

				for s := 0; s < int(index.ItemCount); s++ {
					// calculate string length
					var j int64
					for j = 0; store[int64(j)+o] != 0; j++ {
					}

					vals[s] = string(store[o : o+j])
					o += j + 1
				}

				index.Value = vals
			}

			// save in array
			h.Indexes[x] = index
		}

		// add header
		p.Headers = append(p.Headers, h)

		// calculate location of next header by padding to a multiple of 8
		o := 8 - int(math.Mod(float64(h.Length), 8))

		// seek to next header
		if o > 0 {
			pad := make([]byte, o)
			n, err = r.Read(pad)
			if err != nil {
				return nil, fmt.Errorf("Error seeking beyond header padding of %d bytes: %v", o, err)
			}

			if n != o {
				return nil, fmt.Errorf("Error seeking beyond header padding of %d bytes: only %d bytes returned", o, n)
			}
		}
	}

	return p, nil
}

// dependencies translates the given tag values into a slice of package
// relationships such as provides, conflicts, obsoletes and requires.
func (c *PackageFile) dependencies(nevrsTagId, flagsTagId, namesTagId, versionsTagId int64) Dependencies {
	// TODO: Implement NEVRS tags

	flgs := c.Headers[1].Indexes.GetInts(flagsTagId)
	names := c.Headers[1].Indexes.GetStrings(namesTagId)
	vers := c.Headers[1].Indexes.GetStrings(versionsTagId)

	deps := make(Dependencies, len(names))
	for i := 0; i < len(names); i++ {
		deps[i] = NewDependency(flgs[i], names[i], 0, vers[i], "")
	}

	return deps
}

// String reassembles package metadata to form a standard rpm package name;
// including the package name, version, release and architecture.
func (c *PackageFile) String() string {
	return fmt.Sprintf("%s-%s-%s.%s", c.Name(), c.Version(), c.Release(), c.Architecture())
}

// For tag definitions, see:
// https://github.com/rpm-software-management/rpm/blob/master/lib/rpmtag.h

func (c *PackageFile) Name() string {
	return c.Headers[1].Indexes.GetString(1000)
}

func (c *PackageFile) Version() string {
	return c.Headers[1].Indexes.GetString(1001)
}

func (c *PackageFile) Release() string {
	return c.Headers[1].Indexes.GetString(1002)
}

func (c *PackageFile) Epoch() int64 {
	return c.Headers[1].Indexes.GetInt(1003)
}

func (c *PackageFile) Requires() Dependencies {
	return c.dependencies(5041, 1048, 1049, 1050)
}

func (c *PackageFile) Provides() Dependencies {
	return c.dependencies(5042, 1112, 1047, 1113)
}

func (c *PackageFile) Conflicts() Dependencies {
	return c.dependencies(5044, 1053, 1054, 1055)
}

func (c *PackageFile) Obsoletes() Dependencies {
	return c.dependencies(5043, 1114, 1090, 1115)
}

func (c *PackageFile) Summary() []string {
	return c.Headers[1].Indexes.GetStrings(1004)
}

func (c *PackageFile) Description() []string {
	return c.Headers[1].Indexes.GetStrings(1005)
}

func (c *PackageFile) BuildTime() time.Time {
	return c.Headers[1].Indexes.GetTime(1006)
}

func (c *PackageFile) BuildHost() string {
	return c.Headers[1].Indexes.GetString(1007)
}

func (c *PackageFile) InstallTime() time.Time {
	return c.Headers[1].Indexes.GetTime(1008)
}

func (c *PackageFile) Size() int64 {
	return c.Headers[1].Indexes.GetInt(1009)
}

func (c *PackageFile) Distribution() string {
	return c.Headers[1].Indexes.GetString(1010)
}

func (c *PackageFile) Vendor() string {
	return c.Headers[1].Indexes.GetString(1011)
}

func (c *PackageFile) GIFImage() []byte {
	return c.Headers[1].Indexes.GetBytes(1012)
}

func (c *PackageFile) XPMImage() []byte {
	return c.Headers[1].Indexes.GetBytes(1013)
}

func (c *PackageFile) License() string {
	return c.Headers[1].Indexes.GetString(1014)
}

func (c *PackageFile) PackageFiler() string {
	return c.Headers[1].Indexes.GetString(1015)
}

func (c *PackageFile) Groups() []string {
	return c.Headers[1].Indexes.GetStrings(1016)
}

func (c *PackageFile) ChangeLog() []string {
	return c.Headers[1].Indexes.GetStrings(1017)
}

func (c *PackageFile) Source() []string {
	return c.Headers[1].Indexes.GetStrings(1018)
}

func (c *PackageFile) Patch() []string {
	return c.Headers[1].Indexes.GetStrings(1019)
}

func (c *PackageFile) URL() string {
	return c.Headers[1].Indexes.GetString(1020)
}

func (c *PackageFile) OperatingSystem() string {
	return c.Headers[1].Indexes.GetString(1021)
}

func (c *PackageFile) Architecture() string {
	return c.Headers[1].Indexes.GetString(1022)
}

func (c *PackageFile) PreInstallScript() string {
	return c.Headers[1].Indexes.GetString(1023)
}

func (c *PackageFile) PostInstallScript() string {
	return c.Headers[1].Indexes.GetString(1024)
}

func (c *PackageFile) PreUninstallScript() string {
	return c.Headers[1].Indexes.GetString(1025)
}

func (c *PackageFile) PostUninstallScript() string {
	return c.Headers[1].Indexes.GetString(1026)
}

func (c *PackageFile) OldFilenames() []string {
	return c.Headers[1].Indexes.GetStrings(1027)
}

func (c *PackageFile) Icon() []byte {
	return c.Headers[1].Indexes.GetBytes(1043)
}

func (c *PackageFile) SourceRPM() string {
	return c.Headers[1].Indexes.GetString(1044)
}

func (c *PackageFile) RPMVersion() string {
	return c.Headers[1].Indexes.GetString(1064)
}

func (c *PackageFile) Platform() string {
	return c.Headers[1].Indexes.GetString(1132)
}
