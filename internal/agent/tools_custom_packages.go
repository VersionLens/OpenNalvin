package agent

import (
	"errors"
	"fmt"
	"math"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/open2b/scriggo/native"
)

// customToolPackages returns the set of safe standard library packages
// available to custom tool scripts. Notably absent are os, os/exec, syscall,
// unsafe, net, net/http, runtime, and io/fs — use CallTool for I/O instead.
func customToolPackages() native.Packages {
	return native.Packages{
		"fmt":            customPkgFmt(),
		"strings":        customPkgStrings(),
		"strconv":        customPkgStrconv(),
		"math":           customPkgMath(),
		"sort":           customPkgSort(),
		"errors":         customPkgErrors(),
		"regexp":         customPkgRegexp(),
		"path":           customPkgPath(),
		"path/filepath":  customPkgFilepath(),
		"time":           customPkgTime(),
		"unicode":        customPkgUnicode(),
		"unicode/utf8":   customPkgUTF8(),
	}
}

func customPkgFmt() native.Package {
	return native.Package{
		Name: "fmt",
		Declarations: native.Declarations{
			"Errorf":  fmt.Errorf,
			"Sprintf": fmt.Sprintf,
			"Sscanf":  fmt.Sscanf,
			"Sscan":   fmt.Sscan,
			"Sscanln": fmt.Sscanln,
		},
	}
}

func customPkgStrings() native.Package {
	return native.Package{
		Name: "strings",
		Declarations: native.Declarations{
			"Clone":            strings.Clone,
			"Compare":          strings.Compare,
			"Contains":         strings.Contains,
			"ContainsAny":      strings.ContainsAny,
			"ContainsFunc":     strings.ContainsFunc,
			"ContainsRune":     strings.ContainsRune,
			"Count":            strings.Count,
			"Cut":              strings.Cut,
			"CutPrefix":        strings.CutPrefix,
			"CutSuffix":        strings.CutSuffix,
			"EqualFold":        strings.EqualFold,
			"Fields":           strings.Fields,
			"FieldsFunc":       strings.FieldsFunc,
			"HasPrefix":        strings.HasPrefix,
			"HasSuffix":        strings.HasSuffix,
			"Index":            strings.Index,
			"IndexAny":         strings.IndexAny,
			"IndexByte":        strings.IndexByte,
			"IndexFunc":        strings.IndexFunc,
			"IndexRune":        strings.IndexRune,
			"Join":             strings.Join,
			"LastIndex":        strings.LastIndex,
			"LastIndexAny":     strings.LastIndexAny,
			"LastIndexByte":    strings.LastIndexByte,
			"LastIndexFunc":    strings.LastIndexFunc,
			"Map":              strings.Map,
			"NewReader":        strings.NewReader,
			"NewReplacer":      strings.NewReplacer,
			"Repeat":           strings.Repeat,
			"Replace":          strings.Replace,
			"ReplaceAll":       strings.ReplaceAll,
			"Split":            strings.Split,
			"SplitAfter":       strings.SplitAfter,
			"SplitAfterN":      strings.SplitAfterN,
			"SplitN":           strings.SplitN,
			"ToLower":          strings.ToLower,
			"ToLowerSpecial":   strings.ToLowerSpecial,
			"ToTitle":          strings.ToTitle,
			"ToTitleSpecial":   strings.ToTitleSpecial,
			"ToUpper":          strings.ToUpper,
			"ToUpperSpecial":   strings.ToUpperSpecial,
			"ToValidUTF8":      strings.ToValidUTF8,
			"Trim":             strings.Trim,
			"TrimFunc":         strings.TrimFunc,
			"TrimLeft":         strings.TrimLeft,
			"TrimLeftFunc":     strings.TrimLeftFunc,
			"TrimPrefix":       strings.TrimPrefix,
			"TrimRight":        strings.TrimRight,
			"TrimRightFunc":    strings.TrimRightFunc,
			"TrimSpace":        strings.TrimSpace,
			"TrimSuffix":       strings.TrimSuffix,
		},
	}
}

func customPkgStrconv() native.Package {
	return native.Package{
		Name: "strconv",
		Declarations: native.Declarations{
			"AppendBool":          strconv.AppendBool,
			"AppendFloat":         strconv.AppendFloat,
			"AppendInt":           strconv.AppendInt,
			"AppendQuote":         strconv.AppendQuote,
			"AppendQuoteRune":     strconv.AppendQuoteRune,
			"AppendQuoteRuneToASCII": strconv.AppendQuoteRuneToASCII,
			"AppendQuoteToASCII":  strconv.AppendQuoteToASCII,
			"AppendUint":          strconv.AppendUint,
			"Atoi":                strconv.Atoi,
			"CanBackquote":        strconv.CanBackquote,
			"FormatBool":          strconv.FormatBool,
			"FormatFloat":         strconv.FormatFloat,
			"FormatInt":           strconv.FormatInt,
			"FormatUint":          strconv.FormatUint,
			"IsGraphic":           strconv.IsGraphic,
			"IsPrint":             strconv.IsPrint,
			"Itoa":                strconv.Itoa,
			"ParseBool":           strconv.ParseBool,
			"ParseFloat":          strconv.ParseFloat,
			"ParseInt":            strconv.ParseInt,
			"ParseUint":           strconv.ParseUint,
			"Quote":               strconv.Quote,
			"QuoteRune":           strconv.QuoteRune,
			"QuoteRuneToASCII":    strconv.QuoteRuneToASCII,
			"QuoteRuneToGraphic":  strconv.QuoteRuneToGraphic,
			"QuoteToASCII":        strconv.QuoteToASCII,
			"QuoteToGraphic":      strconv.QuoteToGraphic,
			"QuotedPrefix":        strconv.QuotedPrefix,
			"Unquote":             strconv.Unquote,
			"UnquoteChar":         strconv.UnquoteChar,
		},
	}
}

func customPkgMath() native.Package {
	return native.Package{
		Name: "math",
		Declarations: native.Declarations{
			"Abs":        math.Abs,
			"Acos":       math.Acos,
			"Acosh":      math.Acosh,
			"Asin":       math.Asin,
			"Asinh":      math.Asinh,
			"Atan":       math.Atan,
			"Atan2":      math.Atan2,
			"Atanh":      math.Atanh,
			"Cbrt":       math.Cbrt,
			"Ceil":       math.Ceil,
			"Copysign":   math.Copysign,
			"Cos":        math.Cos,
			"Cosh":       math.Cosh,
			"Dim":        math.Dim,
			"Exp":        math.Exp,
			"Exp2":       math.Exp2,
			"Expm1":      math.Expm1,
			"Floor":      math.Floor,
			"Hypot":      math.Hypot,
			"Inf":        math.Inf,
			"IsInf":      math.IsInf,
			"IsNaN":      math.IsNaN,
			"Log":        math.Log,
			"Log10":      math.Log10,
			"Log1p":      math.Log1p,
			"Log2":       math.Log2,
			"Max":        math.Max,
			"Min":        math.Min,
			"Mod":        math.Mod,
			"NaN":        math.NaN,
			"Pow":        math.Pow,
			"Pow10":      math.Pow10,
			"Round":      math.Round,
			"RoundToEven": math.RoundToEven,
			"Sin":        math.Sin,
			"Sincos":     math.Sincos,
			"Sinh":       math.Sinh,
			"Sqrt":       math.Sqrt,
			"Tan":        math.Tan,
			"Tanh":       math.Tanh,
			"Trunc":      math.Trunc,
			"E":          math.E,
			"Pi":         math.Pi,
			"Phi":        math.Phi,
			"Sqrt2":      math.Sqrt2,
			"SqrtE":      math.SqrtE,
			"SqrtPi":     math.SqrtPi,
			"SqrtPhi":    math.SqrtPhi,
			"Ln2":        math.Ln2,
			"Log2E":      math.Log2E,
			"Ln10":       math.Ln10,
			"Log10E":     math.Log10E,
			"MaxFloat32": math.MaxFloat32,
			"MaxFloat64": math.MaxFloat64,
			"MaxInt":     math.MaxInt,
			"MaxInt8":    math.MaxInt8,
			"MaxInt16":   math.MaxInt16,
			"MaxInt32":   math.MaxInt32,
			"MaxInt64":   math.MaxInt64,
			"MinInt":     math.MinInt,
			"MinInt8":    math.MinInt8,
			"MinInt16":   math.MinInt16,
			"MinInt32":   math.MinInt32,
			"MinInt64":   math.MinInt64,
		},
	}
}

func customPkgSort() native.Package {
	return native.Package{
		Name: "sort",
		Declarations: native.Declarations{
			"Float64s":      sort.Float64s,
			"Float64sAreSorted": sort.Float64sAreSorted,
			"Ints":          sort.Ints,
			"IntsAreSorted": sort.IntsAreSorted,
			"IsSorted":      sort.IsSorted,
			"Search":        sort.Search,
			"SearchFloat64s": sort.SearchFloat64s,
			"SearchInts":    sort.SearchInts,
			"SearchStrings": sort.SearchStrings,
			"Slice":         sort.Slice,
			"SliceIsSorted": sort.SliceIsSorted,
			"SliceStable":   sort.SliceStable,
			"Sort":          sort.Sort,
			"Stable":        sort.Stable,
			"Strings":       sort.Strings,
			"StringsAreSorted": sort.StringsAreSorted,
		},
	}
}

func customPkgErrors() native.Package {
	return native.Package{
		Name: "errors",
		Declarations: native.Declarations{
			"As":     errors.As,
			"Is":     errors.Is,
			"New":    errors.New,
			"Unwrap": errors.Unwrap,
			"Join":   errors.Join,
		},
	}
}

func customPkgRegexp() native.Package {
	return native.Package{
		Name: "regexp",
		Declarations: native.Declarations{
			"Compile":       regexp.Compile,
			"CompilePOSIX":  regexp.CompilePOSIX,
			"Match":         regexp.Match,
			"MatchString":   regexp.MatchString,
			"MustCompile":   regexp.MustCompile,
			"MustCompilePOSIX": regexp.MustCompilePOSIX,
			"QuoteMeta":     regexp.QuoteMeta,
		},
	}
}

func customPkgPath() native.Package {
	return native.Package{
		Name: "path",
		Declarations: native.Declarations{
			"Base":      path.Base,
			"Clean":     path.Clean,
			"Dir":       path.Dir,
			"Ext":       path.Ext,
			"IsAbs":     path.IsAbs,
			"Join":      path.Join,
			"Match":     path.Match,
			"Split":     path.Split,
		},
	}
}

func customPkgFilepath() native.Package {
	return native.Package{
		Name: "filepath",
		Declarations: native.Declarations{
			"Base":        filepath.Base,
			"Clean":       filepath.Clean,
			"Dir":         filepath.Dir,
			"Ext":         filepath.Ext,
			"FromSlash":   filepath.FromSlash,
			"IsAbs":       filepath.IsAbs,
			"IsLocal":     filepath.IsLocal,
			"Join":        filepath.Join,
			"Match":       filepath.Match,
			"Split":       filepath.Split,
			"SplitList":   filepath.SplitList,
			"ToSlash":     filepath.ToSlash,
		},
	}
}

func customPkgTime() native.Package {
	return native.Package{
		Name: "time",
		Declarations: native.Declarations{
			"After":            time.After,
			"Date":             time.Date,
			"Duration":         (*time.Duration)(nil),
			"FixedZone":        time.FixedZone,
			"LoadLocation":     time.LoadLocation,
			"Now":              time.Now,
			"NewTicker":        time.NewTicker,
			"NewTimer":         time.NewTimer,
			"Parse":            time.Parse,
			"ParseDuration":    time.ParseDuration,
			"ParseInLocation":  time.ParseInLocation,
			"Since":            time.Since,
			"Sleep":            time.Sleep,
			"Time":             (*time.Time)(nil),
			"Unix":             time.Unix,
			"Until":            time.Until,
			"Nanosecond":       time.Nanosecond,
			"Microsecond":      time.Microsecond,
			"Millisecond":      time.Millisecond,
			"Second":           time.Second,
			"Minute":           time.Minute,
			"Hour":             time.Hour,
			"Sunday":           time.Sunday,
			"Monday":           time.Monday,
			"Tuesday":          time.Tuesday,
			"Wednesday":        time.Wednesday,
			"Thursday":         time.Thursday,
			"Friday":           time.Friday,
			"Saturday":         time.Saturday,
			"January":          time.January,
			"February":         time.February,
			"March":            time.March,
			"April":            time.April,
			"May":              time.May,
			"June":             time.June,
			"July":             time.July,
			"August":           time.August,
			"September":        time.September,
			"October":          time.October,
			"November":         time.November,
			"December":         time.December,
			"ANSIC":            time.ANSIC,
			"UnixDate":         time.UnixDate,
			"RubyDate":         time.RubyDate,
			"RFC822":           time.RFC822,
			"RFC822Z":          time.RFC822Z,
			"RFC850":           time.RFC850,
			"RFC1123":          time.RFC1123,
			"RFC1123Z":         time.RFC1123Z,
			"RFC3339":          time.RFC3339,
			"RFC3339Nano":      time.RFC3339Nano,
			"Kitchen":          time.Kitchen,
			"Stamp":            time.Stamp,
			"StampMilli":       time.StampMilli,
			"StampMicro":       time.StampMicro,
			"StampNano":        time.StampNano,
			"DateTime":         time.DateTime,
			"DateOnly":         time.DateOnly,
			"TimeOnly":         time.TimeOnly,
			"UTC":              time.UTC,
			"Local":            time.Local,
		},
	}
}

func customPkgUnicode() native.Package {
	return native.Package{
		Name: "unicode",
		Declarations: native.Declarations{
			"IsControl": unicode.IsControl,
			"IsDigit":   unicode.IsDigit,
			"IsGraphic": unicode.IsGraphic,
			"IsLetter":  unicode.IsLetter,
			"IsLower":   unicode.IsLower,
			"IsMark":    unicode.IsMark,
			"IsNumber":  unicode.IsNumber,
			"IsPrint":   unicode.IsPrint,
			"IsPunct":   unicode.IsPunct,
			"IsSpace":   unicode.IsSpace,
			"IsTitle":   unicode.IsTitle,
			"IsUpper":   unicode.IsUpper,
			"To":        unicode.To,
			"ToLower":   unicode.ToLower,
			"ToTitle":   unicode.ToTitle,
			"ToUpper":   unicode.ToUpper,
		},
	}
}

func customPkgUTF8() native.Package {
	return native.Package{
		Name: "utf8",
		Declarations: native.Declarations{
			"AppendRune":       utf8.AppendRune,
			"DecodeLastRune":   utf8.DecodeLastRune,
			"DecodeLastRuneInString": utf8.DecodeLastRuneInString,
			"DecodeRune":       utf8.DecodeRune,
			"DecodeRuneInString": utf8.DecodeRuneInString,
			"EncodeRune":       utf8.EncodeRune,
			"FullRune":         utf8.FullRune,
			"FullRuneInString": utf8.FullRuneInString,
			"RuneCount":        utf8.RuneCount,
			"RuneCountInString": utf8.RuneCountInString,
			"RuneError":        utf8.RuneError,
			"RuneLen":          utf8.RuneLen,
			"RuneStart":        utf8.RuneStart,
			"UTFMax":           utf8.UTFMax,
			"Valid":            utf8.Valid,
			"ValidRune":        utf8.ValidRune,
			"ValidString":      utf8.ValidString,
		},
	}
}
