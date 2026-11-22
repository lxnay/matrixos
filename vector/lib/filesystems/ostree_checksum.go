// Port of ostree_checksum_file_at() from ostree-core.c to Go.
//
// Computes the OSTree content checksum for a file, symlink, or directory.
// The checksum covers a GVariant-serialized header (uid, gid, mode,
// symlink target, xattrs) plus the file content for regular files.
package filesystems

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"syscall"
)

// OstreeObjectType mirrors OstreeObjectType from the C library.
type OstreeObjectType int

const (
	OstreeObjectTypeFile    OstreeObjectType = 1
	OstreeObjectTypeDirMeta OstreeObjectType = 3
)

// OstreeChecksumFlags controls checksum behaviour.
type OstreeChecksumFlags int

const (
	OstreeChecksumFlagsNone                 OstreeChecksumFlags = 0
	OstreeChecksumFlagsIgnoreXattrs         OstreeChecksumFlags = 1 << 0
	OstreeChecksumFlagsCanonicalPermissions OstreeChecksumFlags = 1 << 1
)

// Xattr represents a single extended attribute (name includes trailing NUL).
type Xattr struct {
	Name  []byte // NUL-terminated name
	Value []byte
}

// ---------------------------------------------------------------------------
// GVariant serialisation helpers
//
// OSTree stores file metadata in GVariant wire format.  The format uses
// little-endian framing offsets and records integers in the host's native
// order.  Because the C code pre-converts uid/gid/mode with GUINT32_TO_BE
// before constructing the variant, the resulting bytes are always big-endian
// regardless of the host.  We replicate this by writing big-endian directly.
// ---------------------------------------------------------------------------

// gvariantOffsetSize returns the framing-offset width (1, 2, 4 or 8 bytes)
// for a GVariant container whose body is bodySize bytes and which requires
// numOffsets framing offsets.
func gvariantOffsetSize(bodySize, numOffsets int) int {
	for _, osize := range []int{1, 2, 4, 8} {
		total := bodySize + numOffsets*osize
		if osize == 8 || total < (1<<(8*osize)) {
			return osize
		}
	}
	return 8
}

// writeOffset appends a GVariant framing offset of the given width.
func writeOffset(buf *bytes.Buffer, offset, size int) {
	switch size {
	case 1:
		buf.WriteByte(byte(offset))
	case 2:
		var b [2]byte
		binary.LittleEndian.PutUint16(b[:], uint16(offset))
		buf.Write(b[:])
	case 4:
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], uint32(offset))
		buf.Write(b[:])
	case 8:
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], uint64(offset))
		buf.Write(b[:])
	}
}

// serializeXattrTuple serialises a single GVariant (ayay) tuple.
//
// Layout: [name bytes][value bytes][framing: end-of-name]
func serializeXattrTuple(name, value []byte) []byte {
	bodySize := len(name) + len(value)
	// One framing offset for the first child (the second is last-variable).
	osize := gvariantOffsetSize(bodySize, 1)

	var buf bytes.Buffer
	buf.Grow(bodySize + osize)
	buf.Write(name)
	buf.Write(value)
	writeOffset(&buf, len(name), osize)
	return buf.Bytes()
}

// serializeXattrs serialises a GVariant a(ayay).
//
// In GVariant arrays of variable-width elements, ALL element end-offsets
// are stored as trailing framing offsets.
func serializeXattrs(xattrs []Xattr) []byte {
	if len(xattrs) == 0 {
		return nil // empty array = 0 bytes
	}

	elements := make([][]byte, len(xattrs))
	bodySize := 0
	for i, xa := range xattrs {
		elements[i] = serializeXattrTuple(xa.Name, xa.Value)
		bodySize += len(elements[i])
	}

	numOffsets := len(elements)
	osize := gvariantOffsetSize(bodySize, numOffsets)

	var buf bytes.Buffer
	buf.Grow(bodySize + numOffsets*osize)
	for _, e := range elements {
		buf.Write(e)
	}

	// Write end-offsets for every element.
	running := 0
	for _, e := range elements {
		running += len(e)
		writeOffset(&buf, running, osize)
	}
	return buf.Bytes()
}

// buildFileHeader builds the length-prefixed GVariant file header.
//
// GVariant type: (uuuus a(ayay))
//
// The variant is wrapped in a length-prefixed buffer:
//
//	[4 bytes BE size][4 bytes zero padding][variant data]
func buildFileHeader(uid, gid, mode uint32, symlinkTarget string, xattrs []Xattr) ([]byte, error) {
	// Canonicalise: sort xattrs by name.
	sort.Slice(xattrs, func(i, j int) bool {
		return bytes.Compare(xattrs[i].Name, xattrs[j].Name) < 0
	})

	xattrsData := serializeXattrs(xattrs)
	stringSize := len(symlinkTarget) + 1 // NUL-terminated
	bodySize := 16 + stringSize + len(xattrsData)

	// Structure framing: one offset for the string; a(ayay) is last-variable.
	osize := gvariantOffsetSize(bodySize, 1)
	totalSize := bodySize + osize

	var v bytes.Buffer
	v.Grow(totalSize)

	// uid, gid, mode, reserved - stored as big-endian uint32 values
	// (mirrors the C code's GUINT32_TO_BE before g_variant_new).
	if err := binary.Write(&v, binary.BigEndian, uid); err != nil {
		return nil, fmt.Errorf("writing uid: %w", err)
	}
	if err := binary.Write(&v, binary.BigEndian, gid); err != nil {
		return nil, fmt.Errorf("writing gid: %w", err)
	}
	if err := binary.Write(&v, binary.BigEndian, mode); err != nil {
		return nil, fmt.Errorf("writing mode: %w", err)
	}
	if err := binary.Write(&v, binary.BigEndian, uint32(0)); err != nil {
		return nil, fmt.Errorf("writing reserved field: %w", err)
	}

	// NUL-terminated string
	v.WriteString(symlinkTarget)
	v.WriteByte(0)

	// xattrs
	if len(xattrsData) > 0 {
		v.Write(xattrsData)
	}

	// Framing offset: end of string
	writeOffset(&v, 16+stringSize, osize)
	// Wrap in length-prefixed buffer
	variantData := v.Bytes()
	var out bytes.Buffer
	out.Grow(8 + len(variantData))
	if err := binary.Write(&out, binary.BigEndian, uint32(len(variantData))); err != nil {
		return nil, fmt.Errorf("writing variant length: %w", err)
	}
	out.Write([]byte{0, 0, 0, 0}) // 4-byte zero padding (align to 8)
	out.Write(variantData)
	return out.Bytes(), nil
}

// buildDirMeta builds the raw GVariant for directory metadata.
//
// GVariant type: (uuu a(ayay))
//
// Unlike the file header, this is NOT length-prefixed.
func buildDirMeta(uid, gid, mode uint32, xattrs []Xattr) ([]byte, error) {
	sort.Slice(xattrs, func(i, j int) bool {
		return bytes.Compare(xattrs[i].Name, xattrs[j].Name) < 0
	})

	xattrsData := serializeXattrs(xattrs)

	// a(ayay) is the sole variable child and is last -> no framing offsets.
	var buf bytes.Buffer
	buf.Grow(12 + len(xattrsData))
	if err := binary.Write(&buf, binary.BigEndian, uid); err != nil {
		return nil, fmt.Errorf("writing uid: %w", err)
	}
	if err := binary.Write(&buf, binary.BigEndian, gid); err != nil {
		return nil, fmt.Errorf("writing gid: %w", err)
	}
	if err := binary.Write(&buf, binary.BigEndian, mode); err != nil {
		return nil, fmt.Errorf("writing mode: %w", err)
	}
	if len(xattrsData) > 0 {
		buf.Write(xattrsData)
	}
	return buf.Bytes(), nil
}

// ---------------------------------------------------------------------------
// Extended-attribute helpers
// ---------------------------------------------------------------------------

// readXattrs reads all extended attributes of path using sysLlistxattr and
// sysLgetxattr. When strict is true, unexpected llistxattr/lgetxattr errors
// are returned; when false, unsupported or unreadable attributes are skipped.
func readXattrs(path string, strict bool) ([]Xattr, error) {
	sz, err := sysLlistxattr(path, nil)
	if err != nil {
		// No xattr support or no data - treat as empty.
		if !strict || err == syscall.ENOTSUP || err == syscall.ENODATA || err == syscall.ERANGE {
			return nil, nil
		}
		return nil, fmt.Errorf("llistxattr %s: %w", path, err)
	}
	if sz == 0 {
		return nil, nil
	}

	buf := make([]byte, sz)
	sz, err = sysLlistxattr(path, buf)
	if err != nil {
		if !strict {
			return nil, nil
		}
		return nil, fmt.Errorf("llistxattr %s: %w", path, err)
	}

	// Names are NUL-separated with a trailing NUL.
	raw := strings.TrimRight(string(buf[:sz]), "\x00")
	if raw == "" {
		return nil, nil
	}
	names := strings.Split(raw, "\x00")

	xattrs := make([]Xattr, 0, len(names))
	for _, name := range names {
		if name == "" {
			continue
		}
		vsz, err := sysLgetxattr(path, name, nil)
		if err != nil {
			if !strict {
				continue
			}
			return nil, fmt.Errorf("lgetxattr %s %q: %w", path, name, err)
		}
		value := make([]byte, vsz)
		if vsz > 0 {
			if _, err = sysLgetxattr(path, name, value); err != nil {
				if !strict {
					continue
				}
				return nil, fmt.Errorf("lgetxattr %s %q: %w", path, name, err)
			}
		}
		// Name is stored as a NUL-terminated byte array in the GVariant.
		xattrs = append(xattrs, Xattr{
			Name:  append([]byte(name), 0),
			Value: value,
		})
	}
	return xattrs, nil
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// OstreeChecksumFileAt computes the OSTree checksum of a filesystem entry.
//
// For regular files the checksum covers the length-prefixed file header
// followed by the raw file content.  For symlinks it covers only the
// header (which embeds the target).  For directories it covers the raw
// directory-metadata GVariant.
func OstreeChecksumFileAt(
	path string,
	objtype OstreeObjectType,
	flags OstreeChecksumFlags,
) (string, error) {
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		return "", fmt.Errorf("lstat %s: %w", path, err)
	}

	uid := st.Uid
	gid := st.Gid
	mode := st.Mode
	canonPerms := flags&OstreeChecksumFlagsCanonicalPermissions != 0
	ignoreXattrs := flags&OstreeChecksumFlagsIgnoreXattrs != 0

	h := sha256.New()

	isReg := mode&syscall.S_IFMT == syscall.S_IFREG
	isLnk := mode&syscall.S_IFMT == syscall.S_IFLNK
	isDir := mode&syscall.S_IFMT == syscall.S_IFDIR

	// --- directories (dirmeta) -------------------------------------------
	if isDir {
		if canonPerms {
			uid, gid = 0, 0
		}
		var xattrs []Xattr
		if !ignoreXattrs {
			var err error
			if xattrs, err = readXattrs(path, true); err != nil {
				return "", err
			}
		}
		dirMeta, err := buildDirMeta(uid, gid, mode, xattrs)
		if err != nil {
			return "", fmt.Errorf("building dirmeta for %s: %w", path, err)
		}
		h.Write(dirMeta)
		return hex.EncodeToString(h.Sum(nil)), nil
	}

	// --- regular files & symlinks ----------------------------------------
	symlinkTarget := ""
	if isLnk {
		t, err := os.Readlink(path)
		if err != nil {
			return "", fmt.Errorf("readlink %s: %w", path, err)
		}
		symlinkTarget = t
	}

	if isReg && canonPerms {
		uid, gid = 0, 0
	}

	var xattrs []Xattr
	if !ignoreXattrs && objtype == OstreeObjectTypeFile {
		var err error
		if xattrs, err = readXattrs(path, true); err != nil {
			return "", err
		}
	}

	header, err := buildFileHeader(uid, gid, mode, symlinkTarget, xattrs)
	if err != nil {
		return "", fmt.Errorf("building file header for %s: %w", path, err)
	}
	h.Write(header)

	if isReg {
		f, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("open %s: %w", path, err)
		}
		defer f.Close()
		if _, err := io.Copy(h, f); err != nil {
			return "", fmt.Errorf("read %s: %w", path, err)
		}
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
