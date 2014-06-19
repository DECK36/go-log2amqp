package main

import "strconv"

/*
Unescape nginx logs.

Nginx basically does the right thing and escapes all non-ASCII chars in it access logs.
Unfortunately the resulting strings are no longer JSON compatible.
We try to fix that by a) unescaping 'normal' UTF-8 chars and
in case of control chars b) additionally escaping the backslashes.

for JSON  cf. http://tools.ietf.org/html/rfc7159
for Nginx cf. http://nginx.org/en/CHANGES:
Changes with nginx 1.1.6                                         17 Oct 2011
    *) Change: now the 0x7F-0x1F characters are escaped as \xXX in an
       access_log.

Changes with nginx 0.7.0                                         19 May 2008
    *) Change: now the 0x00-0x1F, '"' and '\' characters are escaped as \xXX
       in an access_log.
       Thanks to Maxim Dounin.
*/

func Unescape(input []byte) []byte {
	for i := 0; i < len(input)-3; i++ {
		//j := i
		switch input[i] {
		case '\\':
			switch input[i+1] {
			case '\\': // escaped backslash, should not occur in nginx log but ok, skip 2nd one
				i++
			case 'x': // escaped non-ASCII char
				switch {
				case (input[i+2] == '0' || input[i+2] == '1') || // control char?
					(input[i+2] == '2' && input[i+3] == '2') || // %x22 = "
					(input[i+2] == '5' && input[i+3] == 'C'): // %x5C = \
					// keep escaped notation, but fix \ to \\
					input = append(input[:i], append([]byte{'\\'}, input[i:]...)...)
					i++
				default: // try to unescape
					val, err := strconv.ParseUint(string(input[i+2:i+4]), 16, 8)
					if err != nil {
						// unescape fails -- fix backslash
						input = append(input[:i], append([]byte{'\\'}, input[i:]...)...)
						i++
					} else {
						input[i] = byte(val)
						input = append(input[:i+1], input[i+4:]...)
					}

				}
			default: // invalid single '\'
				input = append(input[:i], append([]byte{'\\'}, input[i:]...)...)
				i++
			}
		}
	}
	return input
}
