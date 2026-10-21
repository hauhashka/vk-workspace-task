#include "msgfmt/msgfmt.hpp"

#include <algorithm>
#include <cstdio>

#include "utf8.hpp"

namespace msgfmt {

using detail::append_utf8;
using detail::is_alnum;
using detail::is_service;
using detail::is_space;
using detail::utf16_units;

const char* style_name(Style style) noexcept {
    switch (style) {
        case Style::Bold: return "bold";
        case Style::Italic: return "italic";
        case Style::Code: return "code";
        case Style::Link: return "link";
    }
    return "";
}

bool is_valid_utf8(std::string_view input) noexcept {
    try {
        std::vector<char32_t> cps;
        return detail::decode_utf8(input, cps);
    } catch (...) {
        return false;
    }
}

namespace {

constexpr std::size_t npos = static_cast<std::size_t>(-1);

unsigned bit(Style style) { return 1u << static_cast<unsigned>(style); }

// Однопроходный разбор. Каждая конструкция ищет свой закрывающий маркер
// только внутри текущего сегмента [begin, end), поэтому диапазоны
// всегда либо вложены, либо не пересекаются.
class Parser {
public:
    explicit Parser(const std::vector<char32_t>& s) : s_(s) {}

    Message run() {
        parse(0, s_.size(), 0);
        std::stable_sort(pending_.begin(), pending_.end(),
                         [](const Pending& a, const Pending& b) {
                             if (a.range.offset != b.range.offset)
                                 return a.range.offset < b.range.offset;
                             if (a.range.length != b.range.length)
                                 return a.range.length > b.range.length;
                             return a.order < b.order;
                         });
        Message msg;
        msg.text = std::move(text_);
        msg.ranges.reserve(pending_.size());
        for (auto& p : pending_) msg.ranges.push_back(std::move(p.range));
        return msg;
    }

private:
    struct Pending {
        Range range;
        std::size_t order;  // порядок открывающего маркера во входе
    };

    const std::vector<char32_t>& s_;
    std::string text_;
    std::size_t pos16_ = 0;
    std::vector<Pending> pending_;
    std::size_t next_order_ = 0;

    void emit(char32_t cp) {
        append_utf8(text_, cp);
        pos16_ += utf16_units(cp);
    }

    bool escaped_pair(std::size_t i, std::size_t end) const {
        return s_[i] == U'\\' && i + 1 < end && is_service(s_[i + 1]);
    }

    // Обёртка вокруг содержимого стиля: запоминает порядок открытия
    // и добавляет диапазон после разбора содержимого.
    template <typename Body>
    void styled(Style style, std::string url, Body&& body) {
        const std::size_t order = next_order_++;
        const std::size_t start = pos16_;
        body();
        pending_.push_back({Range{style, start, pos16_ - start, std::move(url)}, order});
    }

    void parse(std::size_t begin, std::size_t end, unsigned active) {
        std::size_t i = begin;
        while (i < end) {
            const char32_t c = s_[i];
            if (escaped_pair(i, end)) {
                emit(s_[i + 1]);
                i += 2;
                continue;
            }
            std::size_t next = npos;
            if (c == U'*' && i + 1 < end && s_[i + 1] == U'*') {
                if (!(active & bit(Style::Bold))) next = try_bold(i, end, active);
                if (next == npos) {
                    emit(U'*');
                    emit(U'*');
                    i += 2;
                    continue;
                }
            } else if (c == U'_' && !(active & bit(Style::Italic))) {
                next = try_italic(i, end, active);
            } else if (c == U'`' && !(active & bit(Style::Code))) {
                next = try_code(i, end);
            } else if (c == U'[' && !(active & bit(Style::Link))) {
                next = try_link(i, end, active);
            }
            if (next != npos) {
                i = next;
            } else {
                emit(c);
                ++i;
            }
        }
    }

    // Каждая try_* возвращает позицию после конструкции или npos,
    // если конструкция не распознана (тогда маркер выводится как текст).

    std::size_t try_bold(std::size_t i, std::size_t end, unsigned active) {
        const std::size_t from = i + 2;
        if (from >= end || is_space(s_[from])) return npos;
        std::size_t k = from;
        while (k + 1 < end) {
            if (escaped_pair(k, end)) {
                k += 2;
                continue;
            }
            if (k > from && s_[k] == U'*' && s_[k + 1] == U'*' && !is_space(s_[k - 1])) {
                styled(Style::Bold, {}, [&] { parse(from, k, active | bit(Style::Bold)); });
                return k + 2;
            }
            ++k;
        }
        return npos;
    }

    std::size_t try_italic(std::size_t i, std::size_t end, unsigned active) {
        if (i > 0 && is_alnum(s_[i - 1])) return npos;
        const std::size_t from = i + 1;
        if (from >= end || is_space(s_[from])) return npos;
        std::size_t k = from;
        while (k < end) {
            if (escaped_pair(k, end)) {
                k += 2;
                continue;
            }
            if (k > from && s_[k] == U'_' && !is_space(s_[k - 1]) &&
                (k + 1 >= s_.size() || !is_alnum(s_[k + 1]))) {
                styled(Style::Italic, {}, [&] { parse(from, k, active | bit(Style::Italic)); });
                return k + 1;
            }
            ++k;
        }
        return npos;
    }

    std::size_t try_code(std::size_t i, std::size_t end) {
        const std::size_t from = i + 1;
        if (from >= end || is_space(s_[from])) return npos;
        // Внутри code экранирование не действует - ищем ближайшую кавычку.
        for (std::size_t k = from + 1; k < end; ++k) {
            if (s_[k] == U'`' && !is_space(s_[k - 1])) {
                styled(Style::Code, {}, [&] {
                    for (std::size_t j = from; j < k; ++j) emit(s_[j]);
                });
                return k + 1;
            }
        }
        return npos;
    }

    std::size_t try_link(std::size_t i, std::size_t end, unsigned active) {
        const std::size_t from = i + 1;
        if (from >= end || is_space(s_[from])) return npos;
        // Текст ссылки не содержит ']' - берём ближайшую неэкранированную.
        std::size_t close = npos;
        for (std::size_t k = from; k < end;) {
            if (escaped_pair(k, end)) {
                k += 2;
                continue;
            }
            if (s_[k] == U']') {
                close = k;
                break;
            }
            ++k;
        }
        if (close == npos || close == from || is_space(s_[close - 1])) return npos;
        if (close + 1 >= end || s_[close + 1] != U'(') return npos;
        const std::size_t url_from = close + 2;
        std::size_t k = url_from;
        while (k < end && s_[k] != U')') {
            if (is_space(s_[k])) return npos;
            ++k;
        }
        if (k >= end || k == url_from) return npos;
        std::string url;
        for (std::size_t j = url_from; j < k; ++j) append_utf8(url, s_[j]);
        styled(Style::Link, std::move(url),
               [&] { parse(from, close, active | bit(Style::Link)); });
        return k + 1;
    }
};

std::vector<char32_t> decode_or_throw(std::string_view input) {
    std::vector<char32_t> cps;
    if (!detail::decode_utf8(input, cps)) throw InvalidUtf8();
    return cps;
}

void append_json_string(std::string& out, std::string_view s) {
    out.push_back('"');
    for (const char ch : s) {
        const auto c = static_cast<unsigned char>(ch);
        switch (c) {
            case '"': out += "\\\""; break;
            case '\\': out += "\\\\"; break;
            case '\b': out += "\\b"; break;
            case '\f': out += "\\f"; break;
            case '\n': out += "\\n"; break;
            case '\r': out += "\\r"; break;
            case '\t': out += "\\t"; break;
            default:
                if (c < 0x20 || c == 0x7F) {
                    char buf[8];
                    std::snprintf(buf, sizeof buf, "\\u%04x", static_cast<unsigned>(c));
                    out += buf;
                } else {
                    out.push_back(ch);  // UTF-8 уже проверен и передаётся как есть
                }
        }
    }
    out.push_back('"');
}

}  // namespace

Message parse(std::string_view input) {
    const std::vector<char32_t> cps = decode_or_throw(input);
    return Parser(cps).run();
}

std::string escape(std::string_view input) {
    const std::vector<char32_t> cps = decode_or_throw(input);
    std::string out;
    out.reserve(input.size() + input.size() / 4);
    for (const char32_t cp : cps) {
        if (is_service(cp)) out.push_back('\\');
        append_utf8(out, cp);
    }
    return out;
}

std::string to_json(const Message& message) {
    std::string out = "{\"text\": ";
    append_json_string(out, message.text);
    out += ", \"ranges\": [";
    bool first = true;
    for (const auto& r : message.ranges) {
        if (!first) out += ", ";
        first = false;
        out += "{\"style\": ";
        append_json_string(out, style_name(r.style));
        out += ", \"offset\": " + std::to_string(r.offset);
        out += ", \"length\": " + std::to_string(r.length);
        out += ", \"url\": ";
        append_json_string(out, r.url);
        out += "}";
    }
    out += "]}";
    return out;
}

}  // namespace msgfmt
