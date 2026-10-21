// msgfmt: преобразование разметки сообщения в пару «чистый текст + стилевые диапазоны».
//
// Позиции и длины диапазонов измеряются в кодовых единицах UTF-16,
// текст на входе и выходе - UTF-8.
#pragma once

#include <cstddef>
#include <stdexcept>
#include <string>
#include <string_view>
#include <vector>

namespace msgfmt {

enum class Style { Bold, Italic, Code, Link };

// Имя стиля в JSON: "bold", "italic", "code", "link".
const char* style_name(Style style) noexcept;

struct Range {
    Style style;
    std::size_t offset;  // в кодовых единицах UTF-16
    std::size_t length;  // в кодовых единицах UTF-16
    std::string url;     // непусто только для Style::Link

    bool operator==(const Range& other) const {
        return style == other.style && offset == other.offset &&
               length == other.length && url == other.url;
    }
};

struct Message {
    std::string text;           // чистый текст, UTF-8
    std::vector<Range> ranges;  // отсортированы по (offset ↑, length ↓, порядок открытия ↑)
};

// Единственная предусмотренная ошибка - некорректный UTF-8 на входе.
class InvalidUtf8 : public std::runtime_error {
public:
    InvalidUtf8() : std::runtime_error("invalid_utf8") {}
};

// Строгая проверка UTF-8: без overlong-форм, суррогатов и кодов выше U+10FFFF.
bool is_valid_utf8(std::string_view input) noexcept;

// Разбор разметки. Бросает InvalidUtf8.
Message parse(std::string_view input);

// Экранирование: перед каждым служебным символом ставится обратный слеш.
// Для любой корректной строки s: parse(escape(s)).text == s и диапазонов нет.
// Бросает InvalidUtf8.
std::string escape(std::string_view input);

// Сериализация результата в JSON-объект {"text": ..., "ranges": [...]}.
std::string to_json(const Message& message);

}  // namespace msgfmt
