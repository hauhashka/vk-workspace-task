// Внутренние утилиты для работы с UTF-8 и классами символов.
#pragma once

#include <string>
#include <string_view>
#include <vector>

namespace msgfmt::detail {

// Декодирует строгий UTF-8 в последовательность кодовых точек.
// Возвращает false на некорректной последовательности.
bool decode_utf8(std::string_view input, std::vector<char32_t>& out);

void append_utf8(std::string& out, char32_t cp);

inline std::size_t utf16_units(char32_t cp) { return cp >= 0x10000 ? 2 : 1; }

bool is_space(char32_t cp);
bool is_alnum(char32_t cp);

inline bool is_service(char32_t cp) {
    switch (cp) {
        case U'*': case U'_': case U'`': case U'[':
        case U']': case U'(': case U')': case U'\\':
            return true;
        default:
            return false;
    }
}

}  // namespace msgfmt::detail
