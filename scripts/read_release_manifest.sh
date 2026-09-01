#!/bin/sh
set -eu

fail() {
	printf 'release manifest validation failed\n' >&2
	exit 1
}

[ "$#" -ge 1 ] && [ "$#" -le 2 ] || fail
manifest=$1
requested_key=${2-}

case "$requested_key" in
	''|IOSBK_VERSION|IOSBK_BUILD_DATE|IOSBK_SOURCE_URL) ;;
	*) fail ;;
esac

[ -f "$manifest" ] && [ ! -L "$manifest" ] && [ -s "$manifest" ] || fail

# The file is data, never shell input. Keep the accepted byte alphabet narrow
# enough that its validated output is also safe for GitHub's single-line env
# file format and for command substitution in build scripts.
if ! LC_ALL=C od -An -v -t u1 "$manifest" | awk '
	{
		for (i = 1; i <= NF; i++) {
			byte = $i + 0
			if ((byte < 32 && byte != 10) || byte > 126) exit 1
		}
	}
'; then
	fail
fi

if ! LC_ALL=C awk -v requested="$requested_key" '
	function valid_core_number(value) {
		return value ~ /^[0-9][0-9]*$/ && (length(value) == 1 || substr(value, 1, 1) != "0")
	}
	function valid_version(value, body, parts, count, prerelease, ids, id_count, i) {
		if (substr(value, 1, 1) != "v") return 0
		body = substr(value, 2)
		count = split(body, parts, "-")
		if (count > 2) return 0
		if (split(parts[1], core, ".") != 3) return 0
		for (i = 1; i <= 3; i++) if (!valid_core_number(core[i])) return 0
		if (count == 1) return 1
		prerelease = parts[2]
		if (prerelease == "" || prerelease !~ /^[0-9A-Za-z.-]+$/) return 0
		id_count = split(prerelease, ids, ".")
		for (i = 1; i <= id_count; i++) {
			if (ids[i] == "" || ids[i] !~ /^[0-9A-Za-z-]+$/) return 0
			if (ids[i] ~ /^[0-9][0-9]*$/ && length(ids[i]) > 1 && substr(ids[i], 1, 1) == "0") return 0
		}
		return 1
	}
	function valid_date(value, parts, year, month, day, days, leap) {
		if (value !~ /^[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]$/) return 0
		split(value, parts, "-")
		year = parts[1] + 0
		month = parts[2] + 0
		day = parts[3] + 0
		if (year < 1970 || month < 1 || month > 12 || day < 1) return 0
		days = 31
		if (month == 4 || month == 6 || month == 9 || month == 11) days = 30
		if (month == 2) {
			leap = (year % 4 == 0 && year % 100 != 0) || year % 400 == 0
			days = leap ? 29 : 28
		}
		return day <= days
	}
	function valid_github_source(value, prefix, rest, slash, owner, repository) {
		prefix = "https://github.com/"
		if (substr(value, 1, length(prefix)) != prefix) return 0
		rest = substr(value, length(prefix) + 1)
		slash = index(rest, "/")
		if (slash < 2 || index(substr(rest, slash + 1), "/") != 0) return 0
		owner = substr(rest, 1, slash - 1)
		repository = substr(rest, slash + 1)
		if (length(owner) > 39 || owner !~ /^[A-Za-z0-9-]+$/) return 0
		if (substr(owner, 1, 1) !~ /^[A-Za-z0-9]$/ || substr(owner, length(owner), 1) !~ /^[A-Za-z0-9]$/) return 0
		if (repository == "." || repository == ".." || length(repository) > 100) return 0
		if (repository !~ /^[A-Za-z0-9._-]+$/ || repository ~ /[.]git$/) return 0
		return 1
	}
	{
		if (invalid) next
		equals = index($0, "=")
		if (equals < 2 || index(substr($0, equals + 1), "=") != 0) {
			invalid = 1
			next
		}
		key = substr($0, 1, equals - 1)
		value = substr($0, equals + 1)
		if (key != "IOSBK_VERSION" && key != "IOSBK_BUILD_DATE" && key != "IOSBK_SOURCE_URL") {
			invalid = 1
			next
		}
		if (value == "" || seen[key]++) {
			invalid = 1
			next
		}
		values[key] = value
	}
	END {
		if (invalid || NR != 3 || seen["IOSBK_VERSION"] != 1 || seen["IOSBK_BUILD_DATE"] != 1 || seen["IOSBK_SOURCE_URL"] != 1) exit 1
		if (!valid_version(values["IOSBK_VERSION"])) exit 1
		if (!valid_date(values["IOSBK_BUILD_DATE"])) exit 1
		if (!valid_github_source(values["IOSBK_SOURCE_URL"])) exit 1
		if (requested != "") {
			print values[requested]
		} else {
			print "IOSBK_VERSION=" values["IOSBK_VERSION"]
			print "IOSBK_BUILD_DATE=" values["IOSBK_BUILD_DATE"]
			print "IOSBK_SOURCE_URL=" values["IOSBK_SOURCE_URL"]
		}
	}
' "$manifest"; then
	fail
fi
