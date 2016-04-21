package git

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"
)

// Resources:
//  https://github.com/git/git/blob/master/Documentation/technical/pack-format.txt
//  http://schacon.github.io/gitbook/7_the_packfile.html

//PackHeader stores version and number of objects in the packfile
// all data is in network-byte order (big-endian)
type PackHeader struct {
	Sig     [4]byte
	Version uint32
	Objects uint32
}

//FanOut table where the "N-th entry of this table records the
// number of objects in the corresponding pack, the first
// byte of whose object name is less than or equal to N.
type FanOut [256]uint32

//Bounds returns the how many objects whose first byte
//has a value of b-1 (in s) and b (returned in e)
//are contained in the fanout table
func (fo FanOut) Bounds(b byte) (s, e int) {
	e = int(fo[b])
	if b > 0 {
		s = int(fo[b-1])
	}
	return
}

//PackIndex represents the git pack file
//index. It is the main object to use for
//opening objects contained in packfiles
//vai OpenObject
type PackIndex struct {
	*os.File

	Version uint32
	FO      FanOut

	shaBase int64
}

//PackFile is git pack file with the actual
//data in it. It should normally not be used
//directly.
type PackFile struct {
	*os.File

	Version  uint32
	ObjCount uint32
}

//PackIndexOpen opens the git pack file with the given
//path. The ".idx" if missing will be appended.
func PackIndexOpen(path string) (*PackIndex, error) {

	if !strings.HasSuffix(path, ".idx") {
		path += ".idx"
	}

	fd, err := os.Open(path)

	if err != nil {
		return nil, fmt.Errorf("git: could not read pack index: %v", err)
	}

	idx := &PackIndex{File: fd, Version: 1}

	var peek [4]byte
	err = binary.Read(fd, binary.BigEndian, &peek)
	if err != nil {
		fd.Close()
		return nil, fmt.Errorf("git: could not read pack index: %v", err)
	}

	if bytes.Equal(peek[:], []byte("\377tOc")) {
		binary.Read(fd, binary.BigEndian, &idx.Version)
	}

	if idx.Version == 1 {
		_, err = idx.Seek(0, 0)
		if err != nil {
			fd.Close()
			return nil, fmt.Errorf("git: io error: %v", err)
		}
	} else if idx.Version > 2 {
		fd.Close()
		return nil, fmt.Errorf("git: unsupported pack index version: %d", idx.Version)
	}

	err = binary.Read(idx, binary.BigEndian, &idx.FO)
	if err != nil {
		idx.Close()
		return nil, fmt.Errorf("git: io error: %v", err)
	}

	idx.shaBase = int64((idx.Version-1)*8) + int64(binary.Size(idx.FO))

	return idx, nil
}

func (pi *PackIndex) ReadSHA1(chksum *SHA1, pos int) error {
	if version := pi.Version; version != 2 {
		return fmt.Errorf("git: v%d version support incomplete", version)
	}

	start := pi.shaBase
	_, err := pi.ReadAt(chksum[0:20], start+int64(pos)*int64(20))
	if err != nil {
		return err
	}

	return nil
}

func (pi *PackIndex) ReadOffset(pos int) (int64, error) {
	if version := pi.Version; version != 2 {
		return -1, fmt.Errorf("git: v%d version incomplete", version)
	}

	//header[2*4] + FanOut[256*4] + n * (sha1[20]+crc[4])
	start := int64(2*4+256*4) + int64(pi.FO[255]*24) + int64(pos*4)

	var offset uint32

	_, err := pi.Seek(start, 0)
	if err != nil {
		return -1, fmt.Errorf("git: io error: %v", err)
	}

	err = binary.Read(pi, binary.BigEndian, &offset)
	if err != nil {
		return -1, err
	}

	//see if msb is set, if so this is an
	// offset into the 64b_offset table
	if val := uint32(1<<31) & offset; val != 0 {
		return -1, fmt.Errorf("git: > 31 bit offests not implemented. Meh")
	}

	return int64(offset), nil
}

func (pi *PackIndex) FindSHA1(target SHA1) (int, error) {

	//s, e and midpoint are one-based indices,
	//where s is the index before interval and
	//e is the index of the last element in it
	//-> search interval is: (s | 1, 2, ... e]
	s, e := pi.FO.Bounds(target[0])

	//invariant: object is, if present, in the interval, (s, e]
	for s < e {
		midpoint := s + (e-s+1)/2

		var sha SHA1
		err := pi.ReadSHA1(&sha, midpoint-1)
		if err != nil {
			return 0, fmt.Errorf("git: io error: %v", err)
		}

		switch bytes.Compare(target[:], sha[:]) {
		case -1: // target < sha1, new interval (s, m-1]
			e = midpoint - 1
		case +1: //taget > sha1, new interval (m, e]
			s = midpoint
		default:
			return midpoint - 1, nil
		}
	}

	return 0, fmt.Errorf("git: sha1 not found in index")
}

func (pi *PackIndex) FindOffset(target SHA1) (int64, error) {

	pos, err := pi.FindSHA1(target)
	if err != nil {
		return 0, err
	}

	off, err := pi.ReadOffset(pos)
	if err != nil {
		return 0, err
	}

	return off, nil
}

func (pi *PackIndex) OpenPackFile() (*PackFile, error) {
	f := pi.Name()
	pf, err := OpenPackFile(f[:len(f)-4] + ".pack")
	if err != nil {
		return nil, err
	}

	return pf, nil
}

//OpenObject will try to find the object with the given id
//in it is index and then reach out to its corresponding
//pack file to open the actual git Object. The returned
//Object needs to be closed by the caller.
//If the object cannot be found it will return an error
//the can be detected via os.IsNotExist()
func (pi *PackIndex) OpenObject(id SHA1) (Object, error) {

	off, err := pi.FindOffset(id)

	if err != nil {
		return nil, err
	}

	pf, err := pi.OpenPackFile()
	if err != nil {
		return nil, err
	}

	obj, err := pf.readRawObject(off)

	if err != nil {
		return nil, err
	}

	if IsStandardObject(obj.otype) {
		err = obj.wrapSourceWithDeflate()
		if err != nil {
			pf.Close()
			return nil, err
		}
		return parseObject(obj)
	}

	if !IsDeltaObject(obj.otype) {
		return nil, fmt.Errorf("git: unsupported object")
	}

	//This is a delta object
	delta, err := pf.parseDelta(obj)

	if err != nil {
		return nil, err
	}

	fmt.Fprintf(os.Stderr, "Building delta chain...\n")
	chain, err := pf.buildDeltaChain(delta, pi)
	fmt.Fprintf(os.Stderr, "Done [%v]\n", err)

	if err != nil {
		return nil, err
	}

	fmt.Fprintf(os.Stderr, "Patching delta\n")
	return pf.patchDelta(chain)
}

//OpenPackFile opens the git pack file at the given path
//It will check the pack file header and version.
//Currently only version 2 is supported.
//NB: This is low-level API and should most likely
//not be used directly.
func OpenPackFile(path string) (*PackFile, error) {
	osfd, err := os.Open(path)

	if err != nil {
		return nil, err
	}

	var header PackHeader
	err = binary.Read(osfd, binary.BigEndian, &header)
	if err != nil {
		return nil, fmt.Errorf("git: could not read header: %v", err)
	}

	if string(header.Sig[:]) != "PACK" {
		return nil, fmt.Errorf("git: packfile signature error")
	}

	if header.Version != 2 {
		return nil, fmt.Errorf("git: unsupported packfile version")
	}

	fd := &PackFile{File: osfd,
		Version:  header.Version,
		ObjCount: header.Objects}

	return fd, nil
}

func (pf *PackFile) readRawObject(offset int64) (gitObject, error) {
	_, err := pf.Seek(offset, 0)
	if err != nil {
		return gitObject{}, fmt.Errorf("git: io error: %v", err)
	}

	b := make([]byte, 1)
	_, err = pf.Read(b)

	if err != nil {
		return gitObject{}, fmt.Errorf("git: io error: %v", err)
	}

	otype := ObjectType((b[0] & 0x70) >> 4)
	size := int64(b[0] & 0xF)

	for i := 0; b[0]&0x80 != 0; i++ {
		// TODO: overflow for i > 9
		_, err = pf.Read(b)
		if err != nil {
			return gitObject{}, fmt.Errorf("git io error: %v", err)
		}

		size += int64(b[0]&0x7F) << uint(4+i*7)
	}

	return gitObject{otype, size, pf}, nil
}

//AsObject reads the git object header at offset and
//then parses the data as the corresponding object type.
//The returned Object will hold onto the packfile handle
//and with closing the Object the packfile will be closed
//too.
func (pf *PackFile) AsObject(offset int64) (Object, error) {

	obj, err := pf.readRawObject(offset)

	if err != nil {
		return nil, err
	}

	if obj.otype > 0 && obj.otype < 5 {
		err = obj.wrapSourceWithDeflate()
		if err != nil {
			return nil, err
		}
	}

	switch obj.otype {
	case ObjCommit:
		return ParseCommit(obj)
	case ObjTree:
		return ParseTree(obj)
	case ObjBlob:
		return ParseBlob(obj)
	case ObjTag:
		return ParseTag(obj)

	case ObjOFSDelta:
		fallthrough
	case OBjRefDelta:
		return pf.parseDelta(obj)

	default:
		return &obj, nil
	}
}

func (pf *PackFile) buildDeltaChain(d *Delta, r idResolver) (*deltaChain, error) {
	var chain deltaChain
	var err error

	for {

		chain.links = append(chain.links, *d)

		if d.otype == OBjRefDelta && d.BaseOff == 0 {
			d.BaseOff, err = r.FindOffset(d.BaseRef)
			if err != nil {
				break
			}
		}

		var obj gitObject
		obj, err = pf.readRawObject(d.BaseOff)
		if err != nil {
			break
		}

		if IsStandardObject(obj.otype) {
			chain.baseObj = obj
			chain.baseOff = d.BaseOff
			break
		} else if !IsDeltaObject(obj.otype) {
			err = fmt.Errorf("git: unexpected object type in delta chain")
			break
		}

		d, err = pf.parseDelta(obj)
	}

	if err != nil {
		//cleanup
		return nil, err
	}

	return &chain, nil
}

func (pf *PackFile) patchDelta(c *deltaChain) (Object, error) {
	fmt.Fprintf(os.Stderr, "Patching delta ...\n")

	_, err := pf.Seek(c.baseOff, os.SEEK_SET)
	if err != nil {
		return nil, err
	}

	r, err := zlib.NewReader(pf)
	if err != nil {
		return nil, err
	}

	defer r.Close()

	ibuf := bytes.NewBuffer(make([]byte, 0, c.baseObj.Size()))

	fmt.Fprintf(os.Stderr, "Delta: base -> buffer\n")
	_, err = io.Copy(ibuf, &io.LimitedReader{R: r, N: c.baseObj.Size()})
	if err != nil {
		return nil, err
	}

	decoder := NewDeltaDecoderReader(nil)

	obuf := bytes.NewBuffer(make([]byte, 0, c.baseObj.Size()))

	for i := len(c.links); i > 0; i-- {
		fmt.Fprintf(os.Stderr, "Delta %d\n", i)
		lk := c.links[i-1]

		_, err := pf.Seek(lk.Offset, os.SEEK_SET)
		if err != nil {
			return nil, err
		}

		r, err := zlib.NewReader(pf)
		if err != nil {
			return nil, err
		}

		decoder.Reset(&io.LimitedReader{R: r, N: lk.Size()})

		ok := decoder.Setup()
		if !ok {
			r.Close()
			return nil, decoder.Err()
		}

		if decoder.TargetSize() > int64(^uint(0)>>1) {
			r.Close()
			return nil, fmt.Errorf("git: target to large for delta unpatching")
		}

		obuf.Grow(int(decoder.TargetSize()))
		obuf.Truncate(0)

		err = decoder.Patch(bytes.NewReader(ibuf.Bytes()), obuf)

		r.Close()

		if err != nil {
			return nil, err
		}

		obuf, ibuf = ibuf, obuf
	}

	//ibuf is holding the data

	obj := gitObject{c.baseObj.otype, int64(ibuf.Len()), ioutil.NopCloser(ibuf)}
	return parseObject(obj)
}
