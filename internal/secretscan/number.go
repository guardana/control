package secretscan

import "strings"

// maxIntegerDigits bounds the integer a number is also read as, so a short
// exponent cannot make the scan write out millions of digits.
const maxIntegerDigits = 400

// integerForm is the decimal integer a JSON number written with a fraction or
// an exponent denotes, as a decoder that reads it back as an integer prints
// it. It reports false for a number that is not an integer, for one written
// without either, and for one of more than maxIntegerDigits digits. The text
// is a valid JSON number; it is read once, whatever its length.
func integerForm(text string) (string, bool) {
	num, ok := parseNumber(text)
	if !ok {
		return "", false
	}
	digit := func(k int) byte {
		if k < len(num.whole) {
			return num.whole[k]
		}
		return num.fraction[k-len(num.whole)]
	}
	n := len(num.whole) + len(num.fraction)
	first := 0
	for first < n && digit(first) == '0' {
		first++
	}
	if first == n {
		return "0", true
	}
	if !num.expOK {
		return "", false
	}
	last := n - 1
	for digit(last) == '0' {
		last--
	}
	zeros := n - 1 - last - len(num.fraction) + num.exp
	if zeros < 0 || last-first+1+zeros > maxIntegerDigits {
		return "", false
	}
	var b strings.Builder
	b.Grow(1 + last - first + 1 + zeros)
	if num.negative {
		b.WriteByte('-')
	}
	for k := first; k <= last; k++ {
		b.WriteByte(digit(k))
	}
	b.WriteString(strings.Repeat("0", zeros))
	return b.String(), true
}

// number is a JSON number's parts. expOK is false for an exponent too large
// for any integer within the bound; only a zero is read as one then.
type number struct {
	negative        bool
	whole, fraction string
	exp             int
	expOK           bool
}

// parseNumber splits a number written with a fraction or an exponent, and
// reports false for any other text.
func parseNumber(text string) (number, bool) {
	num := number{negative: strings.HasPrefix(text, "-"), expOK: true}
	i := 0
	if num.negative {
		i++
	}
	num.whole, i = digitRun(text, i)
	hasFraction := i < len(text) && text[i] == '.'
	if hasFraction {
		num.fraction, i = digitRun(text, i+1)
	}
	hasExponent := i < len(text) && (text[i] == 'e' || text[i] == 'E')
	if hasExponent {
		num.exp, num.expOK = exponent(text[i+1:], len(text)+maxIntegerDigits)
		i = len(text)
	}
	return num, i == len(text) && num.whole != "" && (hasFraction || hasExponent)
}

// digitRun is the run of decimal digits at text[i:] and the index after it.
func digitRun(text string, i int) (string, int) {
	start := i
	for i < len(text) && '0' <= text[i] && text[i] <= '9' {
		i++
	}
	return text[start:i], i
}

// exponent parses an exponent's sign and digits, and reports false for one
// whose magnitude passes limit. No integer of at most maxIntegerDigits
// digits, written in a text of length L, needs an exponent beyond
// L+maxIntegerDigits, so the caller's limit refuses none the bound accepts.
func exponent(text string, limit int) (int, bool) {
	negative := strings.HasPrefix(text, "-")
	if negative || strings.HasPrefix(text, "+") {
		text = text[1:]
	}
	if text == "" {
		return 0, false
	}
	n := 0
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return 0, false
		}
		d := int(text[i] - '0')
		if n > (limit-d)/10 {
			return 0, false
		}
		n = n*10 + d
	}
	if negative {
		n = -n
	}
	return n, true
}
