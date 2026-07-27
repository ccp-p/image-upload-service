//! Minimal JSON parser (zero external dependencies).
//! Handles objects, arrays, strings, numbers, booleans, and null.

#[derive(Debug, Clone)]
pub enum JsonValue {
    Null,
    Bool(bool),
    Number(f64),
    String(String),
    Array(Vec<JsonValue>),
    Object(Vec<(String, JsonValue)>),
}

impl JsonValue {
    pub fn parse(input: &str) -> Result<JsonValue, String> {
        let bytes = input.as_bytes();
        let mut pos = 0;
        let result = parse_value(bytes, &mut pos)?;
        skip_ws(bytes, &mut pos);
        if pos < bytes.len() {
            return Err(format!("Unexpected trailing data at position {}", pos));
        }
        Ok(result)
    }

    pub fn get_str(&self, key: &str) -> Option<&str> {
        match self {
            JsonValue::Object(entries) => {
                for (k, v) in entries {
                    if k.eq_ignore_ascii_case(key) {
                        if let JsonValue::String(s) = v {
                            return Some(s);
                        }
                    }
                }
                None
            }
            _ => None,
        }
    }

    pub fn get_num(&self, key: &str) -> Option<f64> {
        match self {
            JsonValue::Object(entries) => {
                for (k, v) in entries {
                    if k.eq_ignore_ascii_case(key) {
                        if let JsonValue::Number(n) = v {
                            return Some(*n);
                        }
                    }
                }
                None
            }
            _ => None,
        }
    }

    pub fn get_bool(&self, key: &str) -> Option<bool> {
        match self {
            JsonValue::Object(entries) => {
                for (k, v) in entries {
                    if k.eq_ignore_ascii_case(key) {
                        if let JsonValue::Bool(b) = v {
                            return Some(*b);
                        }
                    }
                }
                None
            }
            _ => None,
        }
    }

    pub fn get_array_str(&self, key: &str) -> Option<Vec<String>> {
        match self {
            JsonValue::Object(entries) => {
                for (k, v) in entries {
                    if k.eq_ignore_ascii_case(key) {
                        if let JsonValue::Array(arr) = v {
                            let result: Vec<String> = arr
                                .iter()
                                .filter_map(|item| {
                                    if let JsonValue::String(s) = item {
                                        Some(s.clone())
                                    } else {
                                        None
                                    }
                                })
                                .collect();
                            return Some(result);
                        }
                    }
                }
                None
            }
            _ => None,
        }
    }

    pub fn get_object(&self, key: &str) -> Option<&JsonValue> {
        match self {
            JsonValue::Object(entries) => {
                for (k, v) in entries {
                    if k.eq_ignore_ascii_case(key) {
                        return Some(v);
                    }
                }
                None
            }
            _ => None,
        }
    }

    pub fn to_json_string(&self) -> String {
        match self {
            JsonValue::Null => "null".to_string(),
            JsonValue::Bool(b) => b.to_string(),
            JsonValue::Number(n) => {
                if n.fract() == 0.0 {
                    format!("{}", *n as i64)
                } else {
                    format!("{}", n)
                }
            }
            JsonValue::String(s) => format!("\"{}\"", escape_json_string(s)),
            JsonValue::Array(arr) => {
                let items: Vec<String> = arr.iter().map(|v| v.to_json_string()).collect();
                format!("[{}]", items.join(","))
            }
            JsonValue::Object(entries) => {
                let items: Vec<String> = entries
                    .iter()
                    .map(|(k, v)| format!("\"{}\":{}", escape_json_string(k), v.to_json_string()))
                    .collect();
                format!("{{{}}}", items.join(","))
            }
        }
    }

    pub fn to_json_pretty(&self) -> String {
        let mut out = String::new();
        write_pretty(self, &mut out, 0);
        out
    }
}

fn write_pretty(val: &JsonValue, out: &mut String, indent: usize) {
    let pad = "  ".repeat(indent);
    match val {
        JsonValue::Object(entries) => {
            if entries.is_empty() {
                out.push_str("{}");
            } else {
                out.push_str("{\n");
                for (i, (k, v)) in entries.iter().enumerate() {
                    out.push_str(&"  ".repeat(indent + 1));
                    out.push_str(&format!("\"{}\": ", escape_json_string(k)));
                    write_pretty(v, out, indent + 1);
                    if i < entries.len() - 1 {
                        out.push(',');
                    }
                    out.push('\n');
                }
                out.push_str(&pad);
                out.push('}');
            }
        }
        JsonValue::Array(arr) => {
            if arr.is_empty() {
                out.push_str("[]");
            } else {
                out.push_str("[\n");
                for (i, v) in arr.iter().enumerate() {
                    out.push_str(&"  ".repeat(indent + 1));
                    write_pretty(v, out, indent + 1);
                    if i < arr.len() - 1 {
                        out.push(',');
                    }
                    out.push('\n');
                }
                out.push_str(&pad);
                out.push(']');
            }
        }
        _ => out.push_str(&val.to_json_string()),
    }
}

fn escape_json_string(s: &str) -> String {
    let mut result = String::with_capacity(s.len());
    for c in s.chars() {
        match c {
            '"' => result.push_str("\\\""),
            '\\' => result.push_str("\\\\"),
            '\n' => result.push_str("\\n"),
            '\r' => result.push_str("\\r"),
            '\t' => result.push_str("\\t"),
            _ => result.push(c),
        }
    }
    result
}

fn skip_ws(bytes: &[u8], pos: &mut usize) {
    while *pos < bytes.len() {
        match bytes[*pos] {
            b' ' | b'\t' | b'\n' | b'\r' => *pos += 1,
            _ => break,
        }
    }
}

fn parse_value(bytes: &[u8], pos: &mut usize) -> Result<JsonValue, String> {
    skip_ws(bytes, pos);
    if *pos >= bytes.len() {
        return Err("Unexpected end of input".to_string());
    }
    match bytes[*pos] {
        b'{' => parse_object(bytes, pos),
        b'[' => parse_array(bytes, pos),
        b'"' => parse_string(bytes, pos).map(JsonValue::String),
        b't' | b'f' => parse_bool(bytes, pos),
        b'n' => parse_null(bytes, pos),
        b'-' | b'0'..=b'9' => parse_number(bytes, pos),
        _ => Err(format!(
            "Unexpected character '{}' at position {}",
            bytes[*pos] as char, *pos
        )),
    }
}

fn parse_object(bytes: &[u8], pos: &mut usize) -> Result<JsonValue, String> {
    *pos += 1; // skip '{'
    let mut entries = Vec::new();
    skip_ws(bytes, pos);
    if *pos < bytes.len() && bytes[*pos] == b'}' {
        *pos += 1;
        return Ok(JsonValue::Object(entries));
    }
    loop {
        skip_ws(bytes, pos);
        let key = parse_string(bytes, pos)?;
        skip_ws(bytes, pos);
        if *pos >= bytes.len() || bytes[*pos] != b':' {
            return Err(format!("Expected ':' at position {}", *pos));
        }
        *pos += 1;
        let value = parse_value(bytes, pos)?;
        entries.push((key, value));
        skip_ws(bytes, pos);
        if *pos >= bytes.len() {
            return Err("Unexpected end of input in object".to_string());
        }
        match bytes[*pos] {
            b',' => {
                *pos += 1;
            }
            b'}' => {
                *pos += 1;
                break;
            }
            _ => return Err(format!("Expected ',' or '}}' at position {}", *pos)),
        }
    }
    Ok(JsonValue::Object(entries))
}

fn parse_array(bytes: &[u8], pos: &mut usize) -> Result<JsonValue, String> {
    *pos += 1; // skip '['
    let mut items = Vec::new();
    skip_ws(bytes, pos);
    if *pos < bytes.len() && bytes[*pos] == b']' {
        *pos += 1;
        return Ok(JsonValue::Array(items));
    }
    loop {
        let value = parse_value(bytes, pos)?;
        items.push(value);
        skip_ws(bytes, pos);
        if *pos >= bytes.len() {
            return Err("Unexpected end of input in array".to_string());
        }
        match bytes[*pos] {
            b',' => {
                *pos += 1;
            }
            b']' => {
                *pos += 1;
                break;
            }
            _ => return Err(format!("Expected ',' or ']' at position {}", *pos)),
        }
    }
    Ok(JsonValue::Array(items))
}

fn parse_string(bytes: &[u8], pos: &mut usize) -> Result<String, String> {
    if *pos >= bytes.len() || bytes[*pos] != b'"' {
        return Err(format!("Expected string at position {}", *pos));
    }
    *pos += 1; // skip opening quote
    let mut result = String::new();
    while *pos < bytes.len() {
        match bytes[*pos] {
            b'"' => {
                *pos += 1;
                return Ok(result);
            }
            b'\\' => {
                *pos += 1;
                if *pos >= bytes.len() {
                    return Err("Unexpected end of input in string escape".to_string());
                }
                match bytes[*pos] {
                    b'"' => result.push('"'),
                    b'\\' => result.push('\\'),
                    b'/' => result.push('/'),
                    b'n' => result.push('\n'),
                    b'r' => result.push('\r'),
                    b't' => result.push('\t'),
                    b'b' => result.push('\u{0008}'),
                    b'f' => result.push('\u{000C}'),
                    b'u' => {
                        if *pos + 4 >= bytes.len() {
                            return Err("Invalid unicode escape".to_string());
                        }
                        let hex = std::str::from_utf8(&bytes[*pos + 1..*pos + 5])
                            .map_err(|_| "Invalid unicode escape".to_string())?;
                        let code = u32::from_str_radix(hex, 16)
                            .map_err(|_| "Invalid unicode escape".to_string())?;
                        if let Some(c) = char::from_u32(code) {
                            result.push(c);
                        }
                        *pos += 4;
                    }
                    _ => return Err(format!("Invalid escape sequence at position {}", *pos)),
                }
                *pos += 1;
            }
            _ => {
                result.push(bytes[*pos] as char);
                *pos += 1;
            }
        }
    }
    Err("Unterminated string".to_string())
}

fn parse_bool(bytes: &[u8], pos: &mut usize) -> Result<JsonValue, String> {
    if bytes[*pos..].starts_with(b"true") {
        *pos += 4;
        Ok(JsonValue::Bool(true))
    } else if bytes[*pos..].starts_with(b"false") {
        *pos += 5;
        Ok(JsonValue::Bool(false))
    } else {
        Err(format!("Invalid boolean at position {}", *pos))
    }
}

fn parse_null(bytes: &[u8], pos: &mut usize) -> Result<JsonValue, String> {
    if bytes[*pos..].starts_with(b"null") {
        *pos += 4;
        Ok(JsonValue::Null)
    } else {
        Err(format!("Invalid null at position {}", *pos))
    }
}

fn parse_number(bytes: &[u8], pos: &mut usize) -> Result<JsonValue, String> {
    let start = *pos;
    if *pos < bytes.len() && bytes[*pos] == b'-' {
        *pos += 1;
    }
    while *pos < bytes.len() {
        match bytes[*pos] {
            b'0'..=b'9' | b'.' | b'e' | b'E' | b'+' | b'-' => *pos += 1,
            _ => break,
        }
    }
    let s = std::str::from_utf8(&bytes[start..*pos]).map_err(|_| "Invalid number".to_string())?;
    s.parse::<f64>()
        .map(JsonValue::Number)
        .map_err(|_| format!("Invalid number: {}", s))
}

/// Convenience: parse a JSON object from a file path
pub fn parse_file(path: &str) -> Result<JsonValue, String> {
    let content =
        std::fs::read_to_string(path).map_err(|e| format!("Failed to read {}: {}", path, e))?;
    JsonValue::parse(&content)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_parse_simple_object() {
        let json = r#"{"name": "test", "value": 42, "flag": true, "items": ["a", "b"]}"#;
        let val = JsonValue::parse(json).unwrap();
        assert_eq!(val.get_str("name"), Some("test"));
        assert_eq!(val.get_num("value"), Some(42.0));
        assert_eq!(val.get_bool("flag"), Some(true));
        assert_eq!(
            val.get_array_str("items"),
            Some(vec!["a".into(), "b".into()])
        );
    }

    #[test]
    fn test_parse_nested_object() {
        let json = r#"{"deploy": {"enabled": true, "command": "copy", "paths": ["/a", "/b"]}}"#;
        let val = JsonValue::parse(json).unwrap();
        let deploy = val.get_object("deploy").unwrap();
        assert_eq!(deploy.get_bool("enabled"), Some(true));
        assert_eq!(deploy.get_str("command"), Some("copy"));
        assert_eq!(
            deploy.get_array_str("paths"),
            Some(vec!["/a".into(), "/b".into()])
        );
    }

    #[test]
    fn test_case_insensitive_keys() {
        let json = r#"{"RollbackAfterDeploy": true, "hashLength": 8}"#;
        let val = JsonValue::parse(json).unwrap();
        assert_eq!(val.get_bool("rollbackAfterDeploy"), Some(true));
        assert_eq!(val.get_num("hashLength"), Some(8.0));
    }

    #[test]
    fn test_roundtrip() {
        let json = r#"{"key":"value","num":123,"arr":[1,2,3]}"#;
        let val = JsonValue::parse(json).unwrap();
        let out = val.to_json_string();
        let val2 = JsonValue::parse(&out).unwrap();
        assert_eq!(val2.get_str("key"), Some("value"));
        assert_eq!(val2.get_num("num"), Some(123.0));
    }

    #[test]
    fn test_empty_array_and_object() {
        let val = JsonValue::parse(r#"{"empty_arr": [], "empty_obj": {}}"#).unwrap();
        assert_eq!(val.get_array_str("empty_arr"), Some(vec![]));
        assert!(val.get_object("empty_obj").is_some());
    }

    #[test]
    fn test_escape_sequences() {
        let val = JsonValue::parse(r#"{"path": "C:\\Users\\test"}"#).unwrap();
        assert_eq!(val.get_str("path"), Some("C:\\Users\\test"));
    }
}
