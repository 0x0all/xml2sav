package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"log"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const TimeOffset = 12219379200

const SPSS_NUMERIC = 0

const (
	SPSS_FMT_A         = 1
	SPSS_FMT_F         = 5
	SPSS_FMT_DATE      = 20
	SPSS_FMT_DATE_TIME = 22
)

const (
	SPSS_MLVL_NOM = 1
	SPSS_MLVL_ORD = 2
	SPSS_MLVL_RAT = 3
)

type Label struct {
	Value string
	Desc  string
}

type Var struct {
	Index      int32
	Name       string
	ShortName  string
	Type       int32
	Print      byte
	Width      byte
	Decimals   byte
	Measure    int32
	Label      string
	Default    string
	HasDefault bool
	Labels     []Label
	Value      string
	HasValue   bool
}

var endian = binary.LittleEndian

type SpssWriter struct {
	*bufio.Writer
	seeker   io.WriteSeeker
	Dict     []*Var
	DictMap  map[string]*Var // Long variable names index
	ShortMap map[string]*Var // Short variable names index
	Count    int32           // Number of cases
	Index    int32
}

func NewSpssWriter(w io.WriteSeeker) *SpssWriter {
	return &SpssWriter{
		seeker:   w,
		Writer:   bufio.NewWriter(w),
		DictMap:  make(map[string]*Var),
		ShortMap: make(map[string]*Var),
		Index:    1,
	}
}

func stob(s string, l int) []byte {
	if len(s) > l {
		s = s[:l]
	} else if len(s) < l {
		s += strings.Repeat(" ", l-len(s))
	}
	return []byte(s)
}

func trim(s string, l int) string {
	if len(s) > l {
		return s[:l]
	}
	return s
}

func ftoa(f float64) string {
	return strconv.FormatFloat(f, 'E', -1, 64)
}

func atof(s string) float64 {
	v, err := strconv.ParseFloat(s, 32)
	if err != nil {
		log.Fatalln(err)
	}
	return v
}

var cleanVarNameRegExp = regexp.MustCompile(`[^A-Za-z0-9#\$_\.]`)

func cleanVarName(n string) string {
	n = cleanVarNameRegExp.ReplaceAllLiteralString(n, "")
	if len(n) == 0 {
		n = "illegal"
	}
	if (n[0] < 'a' || n[0] > 'z') && (n[0] < 'A' || n[0] > 'Z') {
		n = "@" + n
	}
	if len(n) > 64 {
		n = n[:64]
	}
	return n
}

func (out *SpssWriter) caseSize() int32 {
	size := int32(0)
	for _, v := range out.Dict {
		size += ((v.Type - 1) / 8) + 1
	}
	return size
}

func (out *SpssWriter) Seek(offset int64, whence int) (int64, error) {
	return out.seeker.Seek(offset, whence)
}

func (out *SpssWriter) VarCount() int32 {
	return int32(len(out.Dict))
}

func (out *SpssWriter) headerRecord(fileLabel string) {
	c := time.Now()
	out.Write(stob("$FL2", 4))                               // rec_tyoe
	out.Write(stob("@(#) SPSS DATA FILE - xml2sav 2.0", 60)) // prod_name
	binary.Write(out, endian, int32(2))                      // layout_code
	binary.Write(out, endian, out.caseSize())                // nominal_case_size
	binary.Write(out, endian, int32(0))                      // compression
	binary.Write(out, endian, int32(0))                      // weight_index
	binary.Write(out, endian, int32(-1))                     // ncases
	binary.Write(out, endian, float64(100))                  // bias
	out.Write(stob(c.Format("02 Jan 06"), 9))                // creation_date
	out.Write(stob(c.Format("15:04:05"), 8))                 // creation_time
	out.Write(stob(fileLabel, 64))                           // file_label
	out.Write(stob("\x00\x00\x00", 3))                       // padding
}

// If you use a buffer, supply it as the flusher argument
// After this close the file
func (out *SpssWriter) updateHeaderNCases() {
	out.Flush()
	out.Seek(80, 0)
	binary.Write(out.seeker, endian, out.Count) // ncases in headerRecord
}

func (out *SpssWriter) variableRecords() {
	for _, v := range out.Dict {
		binary.Write(out, endian, int32(2)) // rec_type
		binary.Write(out, endian, v.Type)   // type (0 or strlen)
		if len(v.Label) > 0 {
			binary.Write(out, endian, int32(1)) // has_var_label
		} else {
			binary.Write(out, endian, int32(0)) // has_var_label
		}
		binary.Write(out, endian, int32(0)) // n_missing_values
		var format int32
		if v.Type > 0 { // string
			format = int32(v.Print)<<16 | int32(v.Type)<<8
		} else { // number
			format = int32(v.Print)<<16 | int32(v.Width)<<8 | int32(v.Decimals)
		}
		binary.Write(out, endian, format) // print
		binary.Write(out, endian, format) // write
		out.Write(stob(v.ShortName, 8))   // name
		if len(v.Label) > 0 {
			binary.Write(out, endian, int32(len(v.Label))) // label_len
			out.Write([]byte(v.Label))                     // label
			pad := (4 - len(v.Label)) % 4
			if pad < 0 {
				pad += 4
			}
			for i := 0; i < pad; i++ {
				out.Write([]byte{0}) // pad out until multiple of 32 bit
			}
		}

		if v.Type > 8 { // handle long string
			count := int((v.Type - 1) / 8) // number of extra vars to store string
			for i := 0; i < count; i++ {
				binary.Write(out, endian, int32(2))  // rec_type
				binary.Write(out, endian, int32(-1)) // extended string part
				binary.Write(out, endian, int32(0))  // has_var_label
				binary.Write(out, endian, int32(0))  // n_missing_valuess
				binary.Write(out, endian, int32(0))  // print
				binary.Write(out, endian, int32(0))  // write
				out.Write(stob("        ", 8))       // name
			}
		}
	}
}

func (out *SpssWriter) valueLabelRecords() {
	for _, v := range out.Dict {
		if len(v.Labels) > 0 && v.Type <= 8 {
			binary.Write(out, endian, int32(3))             // rec_type
			binary.Write(out, endian, int32(len(v.Labels))) // label_count
			for _, label := range v.Labels {
				if v.Type == 0 {
					binary.Write(out, endian, atof(label.Value)) // value
				} else {
					binary.Write(out, endian, stob(label.Value, 8)) // value
				}
				l := len(label.Desc)
				if l > 120 {
					l = 120
				}
				binary.Write(out, endian, byte(l)) // label_len
				out.Write(stob(label.Desc, l))     // label
				pad := (8 - l - 1) % 8
				if pad < 0 {
					pad += 8
				}
				for i := 0; i < pad; i++ {
					out.Write([]byte{32})
				}
			}

			binary.Write(out, endian, int32(4))       // rec_type
			binary.Write(out, endian, int32(1))       // var_count
			binary.Write(out, endian, int32(v.Index)) // vars
		}
	}
}

func (out *SpssWriter) variableDisplayParameterRecord() {
	binary.Write(out, endian, int32(7))         // rec_type
	binary.Write(out, endian, int32(11))        // subtype
	binary.Write(out, endian, int32(4))         // size
	binary.Write(out, endian, out.VarCount()*3) // count
	for _, v := range out.Dict {
		binary.Write(out, endian, v.Measure) // measure
		if v.Type > 0 {
			if v.Type <= int32(maxPrintStringWidth) {
				binary.Write(out, endian, v.Type) // width
			} else {
				binary.Write(out, endian, int32(maxPrintStringWidth)) // width
			}
			binary.Write(out, endian, int32(0)) // alignment (left)
		} else {
			binary.Write(out, endian, int32(8)) // width
			binary.Write(out, endian, int32(1)) // alignment (right)
		}
	}
}

func (out *SpssWriter) longVarNameRecords() {
	binary.Write(out, endian, int32(7))  // rec_type
	binary.Write(out, endian, int32(13)) // subtype
	binary.Write(out, endian, int32(1))  // size

	buf := bytes.Buffer{}
	for i, v := range out.Dict {
		buf.Write([]byte(v.ShortName))
		buf.Write([]byte("="))
		buf.Write([]byte(v.Name))
		if i < len(out.Dict)-1 {
			buf.Write([]byte{9})
		}
	}
	binary.Write(out, endian, int32(buf.Len()))
	out.Write(buf.Bytes())
}

func (out *SpssWriter) encodingRecord() {
	binary.Write(out, endian, int32(7))  // rec_type
	binary.Write(out, endian, int32(20)) // subtype
	binary.Write(out, endian, int32(1))  // size
	binary.Write(out, endian, int32(5))  // filler
	out.Write(stob("UTF-8", 5))          // encoding
}

func (out *SpssWriter) longStringValueLabelsRecord() {
	// Check if we have any
	any := false
	for _, v := range out.Dict {
		if len(v.Labels) > 0 && v.Type > 8 {
			any = true
			break
		}
	}
	if !any {
		return
	}

	// Create record
	buf := new(bytes.Buffer)
	for _, v := range out.Dict {
		if len(v.Labels) > 0 && v.Type > 8 {
			binary.Write(buf, endian, int32(len(v.ShortName))) // var_name_len
			buf.Write([]byte(v.ShortName))                     // var_name
			binary.Write(buf, endian, v.Type)                  // var_width
			binary.Write(buf, endian, int32(len(v.Labels)))    // n_labels
			for _, l := range v.Labels {
				binary.Write(buf, endian, int32(len(l.Value))) // value_len
				buf.Write([]byte(l.Value))                     // value
				binary.Write(buf, endian, int32(len(l.Desc)))  // label_len
				buf.Write([]byte(l.Desc))                      //label
			}
		}
	}

	binary.Write(out, endian, int32(7))         // rec_type
	binary.Write(out, endian, int32(21))        // subtype
	binary.Write(out, endian, int32(1))         // size
	binary.Write(out, endian, int32(buf.Len())) // count
	out.Write(buf.Bytes())
}

func (out *SpssWriter) terminationRecord() {
	binary.Write(out, endian, int32(999)) // rec_type
	binary.Write(out, endian, int32(0))   // filler
}

func (out *SpssWriter) AddVar(v *Var) {
	if v.Type > 255 {
		log.Fatalln("Maximum length for a variable is 255,", v.Name, "is", v.Type)
	}

	// Clean variable name
	name := cleanVarName(v.Name)
	if name != v.Name {
		log.Printf("Change variable name '%s' to '%s'\n", v.Name, name)
		v.Name = name
	}

	if _, found := out.DictMap[v.Name]; found {
		log.Fatalln("Adding duplicate variable named", v.Name)
	}

	v.Index = out.Index
	out.Index += ((v.Type - 1) / 8) + 1

	// Create unique short variable name
	short := strings.ToUpper(v.Name)
	count := 1
	if len(short) > 8 {
		short = short[:8]
	}
	for {
		_, found := out.ShortMap[short]
		if !found {
			break
		}
		count++
		s := strconv.Itoa(count)
		if len(short)+len(s) > 8 {
			short = short[:8-len(s)]
		}
		short = short + s
	}
	v.ShortName = short

	out.Dict = append(out.Dict, v)
	out.DictMap[v.Name] = v
	out.ShortMap[v.ShortName] = v
}

func (out *SpssWriter) ClearCase() {
	for _, v := range out.Dict {
		v.Value = ""
		v.HasValue = false
	}
}

func (out *SpssWriter) SetVar(name, value string) {
	v, found := out.DictMap[name]
	if !found {
		log.Fatalln("Can not find the variable named", name)
	}
	v.Value = value
	v.HasValue = true
}

func (out *SpssWriter) WriteCase() {
	for _, v := range out.Dict {
		if v.HasValue || v.HasDefault {
			var val string
			if v.HasValue {
				val = v.Value
			} else {
				val = v.Default
			}

			if v.Type > 0 { // string
				if len(val) > int(v.Type) {
					val = val[:v.Type]
					log.Printf("Truncated string for %s: %s\n", v.Name, val)
				}
				ll := (((int(v.Type) - 1) / 8) + 1) * 8
				out.Write(stob(val, ll))
			} else if v.Print == SPSS_FMT_DATE {
				if val == "" {
					binary.Write(out, endian, -math.MaxFloat64) // Write missing
				} else {
					t, err := time.Parse("2-Jan-2006", v.Value)
					if err != nil {
						log.Printf("Problem pasing value for %s: %s - set as missing\n", v.Name, err)
						binary.Write(out, endian, -math.MaxFloat64) // Write missing
					} else {
						binary.Write(out, endian, float64(t.Unix()+TimeOffset))
					}
				}
			} else if v.Print == SPSS_FMT_DATE_TIME {
				if val == "" {
					binary.Write(out, endian, -math.MaxFloat64) // Write missing
				} else {
					t, err := time.Parse("2-Jan-2006 15:04:05", v.Value)
					if err != nil {
						log.Printf("Problem pasing value for %s: %s - set as missing\n", v.Name, err)
						binary.Write(out, endian, -math.MaxFloat64) // Write missing
					} else {
						binary.Write(out, endian, float64(t.Unix()+TimeOffset))
					}
				}
			} else { // number
				if val == "" {
					binary.Write(out, endian, -math.MaxFloat64) // Write missing
				} else {
					f, err := strconv.ParseFloat(val, 64)
					if err != nil {
						log.Printf("Problem pasing value for %s: %s - set as missing\n", v.Name, err)
						binary.Write(out, endian, -math.MaxFloat64) // Write missing
					} else {
						binary.Write(out, endian, f)
					}
				}
			}
		} else { // Write missing value
			if v.Type > 0 {
				out.Write(stob("", (((int(v.Type)-1)/8)+1)*8))
			} else {
				binary.Write(out, endian, -math.MaxFloat64)
			}
		}
	}
	out.Count++
}

func (out *SpssWriter) Start(fileLabel string) {
	out.headerRecord(fileLabel)
	out.variableRecords()
	out.valueLabelRecords()
	out.variableDisplayParameterRecord()
	out.longVarNameRecords()
	out.encodingRecord()
	out.longStringValueLabelsRecord()
	out.terminationRecord()
}

func (out *SpssWriter) Finish() {
	out.updateHeaderNCases()
}