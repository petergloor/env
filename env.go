package env

import (
	"encoding"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrNotAStructPtr is returned if you pass something that is not a pointer to a
	// Struct to Parse
	ErrNotAStructPtr = errors.New("Expected a pointer to a Struct")
	// ErrUnsupportedType if the struct field type is not supported by env
	ErrUnsupportedType = errors.New("Type is not supported")
	// ErrUnsupportedSliceType if the slice element type is not supported by env
	ErrUnsupportedSliceType = errors.New("Unsupported slice type")
	// OnEnvVarSet is an optional convenience callback, such as for logging purposes.
	// If not nil, it's called after successfully setting the given field from the given value.
	OnEnvVarSet func(reflect.StructField, string)
	// Friendly names for reflect types
	sliceOfInts      = reflect.TypeOf([]int(nil))
	sliceOfInt64s    = reflect.TypeOf([]int64(nil))
	sliceOfUint64s   = reflect.TypeOf([]uint64(nil))
	sliceOfStrings   = reflect.TypeOf([]string(nil))
	sliceOfBools     = reflect.TypeOf([]bool(nil))
	sliceOfFloat32s  = reflect.TypeOf([]float32(nil))
	sliceOfFloat64s  = reflect.TypeOf([]float64(nil))
	sliceOfDurations = reflect.TypeOf([]time.Duration(nil))
	sliceOfURLs      = reflect.TypeOf([]url.URL(nil))
)

// CustomParsers is a friendly name for the type that `ParseWithFuncs()` accepts
type CustomParsers map[reflect.Type]ParserFunc

// ParserFunc defines the signature of a function that can be used within `CustomParsers`
type ParserFunc func(v string) (interface{}, error)


// Set - sets an environment variable
func Set(key, value string) error {
	return os.Setenv(key, value)
}

// Unset - unsets an environment variable
func Unset(key string) error {
	return os.Unsetenv(key)
}

// Get - get an environment variable, empty string if does not exist
func Get(key string) string {
	return os.Getenv(key)
}


// GetOr - get an environment variable or return default value if does not exist
func GetOr(key, defaultValue string) string {
	value, ok := os.LookupEnv(key)
	if ok {
		return value
	}
	return defaultValue
}

// MustGet - get an environment variable or panic if does not exist
func MustGet(key, defaultValue string) string {
	value, ok := os.LookupEnv(key)
	if ok {
		return value
	}
	panic(fmt.Sprintf("expected environment variable \"%s\" does not exist", key))
}


// Parse parses a struct containing `env` tags and loads its values from
// environment variables.
func Parse(v interface{}) error {
	return ParseWithPrefixFuncs(v, "", make(map[reflect.Type]ParserFunc, 0))
}

// ParseWithPrefix parses a struct containing `env` tags and loads its values from
// environment variables.  The actual env vars looked up include the passed in prefix.
func ParseWithPrefix(v interface{}, prefix string) error {
	return ParseWithPrefixFuncs(v, prefix, make(map[reflect.Type]ParserFunc, 0))
}

// ParseWithFuncs is the same as `Parse` except it also allows the user to pass
// in custom parsers.
func ParseWithFuncs(v interface{}, funcMap CustomParsers) error {
	return ParseWithPrefixFuncs(v, "", funcMap)
}

// ParseWithPrefixFuncs is the same as `ParseWithPrefix` except it also allows the user to pass
// in custom parsers.
func ParseWithPrefixFuncs(v interface{}, prefix string, funcMap CustomParsers) error {
	ptrRef := reflect.ValueOf(v)
	if ptrRef.Kind() != reflect.Ptr {
		return ErrNotAStructPtr
	}
	ref := ptrRef.Elem()
	if ref.Kind() != reflect.Struct {
		return ErrNotAStructPtr
	}
	return doParse(ref, prefix, funcMap)
}

func doParse(ref reflect.Value, prefix string, funcMap CustomParsers) error {
	refType := ref.Type()
	var errorList []string

	for i := 0; i < refType.NumField(); i++ {
		refField := ref.Field(i)
		if reflect.Ptr == refField.Kind() && !refField.IsNil() && refField.CanSet() {
			err := Parse(refField.Interface())
			if nil != err {
				return err
			}
			continue
		}
		refTypeField := refType.Field(i)
		value, err := get(refTypeField, prefix)
		if err != nil {
			errorList = append(errorList, err.Error())
			continue
		}
		if value == "" {
			continue
		}
		if err := set(refField, refTypeField, value, funcMap); err != nil {
			errorList = append(errorList, err.Error())
			continue
		}
		if OnEnvVarSet != nil {
			OnEnvVarSet(refTypeField, value)
		}
	}
	if len(errorList) == 0 {
		return nil
	}
	return errors.New(strings.Join(errorList, ". "))
}

func get(field reflect.StructField, prefix string) (string, error) {
	var (
		val string
		err error
	)

	key, opts := parseKeyForOption(field.Tag.Get("env"))

	defaultValue := field.Tag.Get("envDefault")
	val = getOrWithPrefix(key, prefix, defaultValue)

	expandVar := field.Tag.Get("envExpand")
	if strings.ToLower(expandVar) == "true" {
		val = os.ExpandEnv(val)
	}

	if len(opts) > 0 {
		for _, opt := range opts {
			// The only option supported is "required".
			switch opt {
			case "":
				break
			case "required":
				val, err = getRequired(key, prefix)
			default:
				err = fmt.Errorf("env tag option %q not supported", opt)
			}
		}
	}

	return val, err
}

// split the env tag's key into the expected key and desired option, if any.
func parseKeyForOption(key string) (string, []string) {
	opts := strings.Split(key, ",")
	return opts[0], opts[1:]
}

func getRequired(key, prefix string) (string, error) {
	if value, ok := os.LookupEnv(prefix + key); ok {
		return value, nil
	}
	return "", fmt.Errorf("required environment variable %q is not set", key)
}

func getOrWithPrefix(key, prefix, defaultValue string) string {
	value, ok := os.LookupEnv(prefix + key)
	if ok {
		return value
	}
	return defaultValue
}

func set(field reflect.Value, refType reflect.StructField, value string, funcMap CustomParsers) error {
	// use custom parser if configured for this type
	parserFunc, ok := funcMap[refType.Type]
	if ok {
		val, err := parserFunc(value)
		if err != nil {
			return fmt.Errorf("Custom parser error: %v", err)
		}
		field.Set(reflect.ValueOf(val))
		return nil
	}

	if refType.Type == reflect.TypeOf(url.URL{}) {
		u, err := url.Parse(value)
		if err != nil {
			return fmt.Errorf("Unable to complete URL parse: %v", err)
		}
		field.Set(reflect.ValueOf(*u))
		return nil
	}

	// fall back to built-in parsers
	switch field.Kind() {
	case reflect.Slice:
		separator := refType.Tag.Get("envSeparator")
		return handleSlice(field, value, separator)
	case reflect.String:
		field.SetString(value)
	case reflect.Bool:
		bvalue, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		field.SetBool(bvalue)
	case reflect.Int:
		intValue, err := strconv.ParseInt(value, 10, 32)
		if err != nil {
			return err
		}
		field.SetInt(intValue)
	case reflect.Uint:
		uintValue, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return err
		}
		field.SetUint(uintValue)
	case reflect.Float32:
		v, err := strconv.ParseFloat(value, 32)
		if err != nil {
			return err
		}
		field.SetFloat(v)
	case reflect.Float64:
		v, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return err
		}
		field.Set(reflect.ValueOf(v))
	case reflect.Int64:
		if refType.Type.String() == "time.Duration" {
			dValue, err := time.ParseDuration(value)
			if err != nil {
				return err
			}
			field.Set(reflect.ValueOf(dValue))
		} else {
			intValue, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return err
			}
			field.SetInt(intValue)
		}
	case reflect.Uint64:
		uintValue, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return err
		}
		field.SetUint(uintValue)
	default:
		return handleTextUnmarshaler(field, value)
	}
	return nil
}

func handleSlice(field reflect.Value, value, separator string) error {
	if separator == "" {
		separator = ","
	}

	splitData := strings.Split(value, separator)

	switch field.Type() {
	case sliceOfStrings:
		field.Set(reflect.ValueOf(splitData))
	case sliceOfInts:
		intData, err := parseInts(splitData)
		if err != nil {
			return err
		}
		field.Set(reflect.ValueOf(intData))
	case sliceOfInt64s:
		int64Data, err := parseInt64s(splitData)
		if err != nil {
			return err
		}
		field.Set(reflect.ValueOf(int64Data))
	case sliceOfUint64s:
		uint64Data, err := parseUint64s(splitData)
		if err != nil {
			return err
		}
		field.Set(reflect.ValueOf(uint64Data))
	case sliceOfFloat32s:
		data, err := parseFloat32s(splitData)
		if err != nil {
			return err
		}
		field.Set(reflect.ValueOf(data))
	case sliceOfFloat64s:
		data, err := parseFloat64s(splitData)
		if err != nil {
			return err
		}
		field.Set(reflect.ValueOf(data))
	case sliceOfBools:
		boolData, err := parseBools(splitData)
		if err != nil {
			return err
		}
		field.Set(reflect.ValueOf(boolData))
	case sliceOfDurations:
		durationData, err := parseDurations(splitData)
		if err != nil {
			return err
		}
		field.Set(reflect.ValueOf(durationData))
	case sliceOfURLs:
		urlData, err := parseUrls(splitData)
		if err != nil {
			return err
		}
		field.Set(reflect.ValueOf(urlData))
	default:
		elemType := field.Type().Elem()
		// Ensure we test *type as we can always address elements in a slice.
		if elemType.Kind() == reflect.Ptr {
			elemType = elemType.Elem()
		}
		if _, ok := reflect.New(elemType).Interface().(encoding.TextUnmarshaler); !ok {
			return ErrUnsupportedSliceType
		}
		return parseTextUnmarshalers(field, splitData)

	}
	return nil
}

func handleTextUnmarshaler(field reflect.Value, value string) error {
	if reflect.Ptr == field.Kind() {
		if field.IsNil() {
			field.Set(reflect.New(field.Type().Elem()))
		}
	} else if field.CanAddr() {
		field = field.Addr()
	}

	tm, ok := field.Interface().(encoding.TextUnmarshaler)
	if !ok {
		return ErrUnsupportedType
	}

	return tm.UnmarshalText([]byte(value))
}

func parseInts(data []string) ([]int, error) {
	intSlice := make([]int, 0, len(data))

	for _, v := range data {
		intValue, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			return nil, err
		}
		intSlice = append(intSlice, int(intValue))
	}
	return intSlice, nil
}

func parseInt64s(data []string) ([]int64, error) {
	intSlice := make([]int64, 0, len(data))

	for _, v := range data {
		intValue, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, err
		}
		intSlice = append(intSlice, int64(intValue))
	}
	return intSlice, nil
}

func parseUint64s(data []string) ([]uint64, error) {
	var uintSlice []uint64

	for _, v := range data {
		uintValue, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return nil, err
		}
		uintSlice = append(uintSlice, uint64(uintValue))
	}
	return uintSlice, nil
}

func parseFloat32s(data []string) ([]float32, error) {
	float32Slice := make([]float32, 0, len(data))

	for _, v := range data {
		data, err := strconv.ParseFloat(v, 32)
		if err != nil {
			return nil, err
		}
		float32Slice = append(float32Slice, float32(data))
	}
	return float32Slice, nil
}

func parseFloat64s(data []string) ([]float64, error) {
	float64Slice := make([]float64, 0, len(data))

	for _, v := range data {
		data, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, err
		}
		float64Slice = append(float64Slice, float64(data))
	}
	return float64Slice, nil
}

func parseBools(data []string) ([]bool, error) {
	boolSlice := make([]bool, 0, len(data))

	for _, v := range data {
		bvalue, err := strconv.ParseBool(v)
		if err != nil {
			return nil, err
		}

		boolSlice = append(boolSlice, bvalue)
	}
	return boolSlice, nil
}

func parseDurations(data []string) ([]time.Duration, error) {
	durationSlice := make([]time.Duration, 0, len(data))

	for _, v := range data {
		dvalue, err := time.ParseDuration(v)
		if err != nil {
			return nil, err
		}

		durationSlice = append(durationSlice, dvalue)
	}
	return durationSlice, nil
}

func parseUrls(data []string) ([]url.URL, error) {
	urlSlice := make([]url.URL, 0, len(data))

	for _, v := range data {
		uvalue, err := url.Parse(v)
		if err != nil {
			return nil, err
		}

		urlSlice = append(urlSlice, *uvalue)
	}
	return urlSlice, nil
}

func parseTextUnmarshalers(field reflect.Value, data []string) error {
	s := len(data)
	elemType := field.Type().Elem()
	slice := reflect.MakeSlice(reflect.SliceOf(elemType), s, s)
	for i, v := range data {
		sv := slice.Index(i)
		kind := sv.Kind()
		if kind == reflect.Ptr {
			sv = reflect.New(elemType.Elem())
		} else {
			sv = sv.Addr()
		}
		tm := sv.Interface().(encoding.TextUnmarshaler)
		if err := tm.UnmarshalText([]byte(v)); err != nil {
			return err
		}
		if kind == reflect.Ptr {
			slice.Index(i).Set(sv)
		}
	}

	field.Set(slice)

	return nil
}
