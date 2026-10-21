// Юнит-тесты msgfmt без сторонних фреймворков.
#include <cstdint>
#include <iostream>
#include <random>
#include <string>
#include <vector>

#include "msgfmt/msgfmt.hpp"

using msgfmt::Range;
using msgfmt::Style;

namespace {

int g_failures = 0;
int g_checks = 0;

std::string show(const std::vector<Range>& ranges) {
    std::string out = "[";
    for (const auto& r : ranges) {
        out += std::string(msgfmt::style_name(r.style)) + "(" + std::to_string(r.offset) + "," +
               std::to_string(r.length) + (r.url.empty() ? "" : "," + r.url) + ") ";
    }
    return out + "]";
}

#define CHECK(cond)                                                              \
    do {                                                                         \
        ++g_checks;                                                              \
        if (!(cond)) {                                                           \
            ++g_failures;                                                        \
            std::cerr << __FILE__ << ":" << __LINE__ << ": CHECK failed: " #cond \
                      << "\n";                                                   \
        }                                                                        \
    } while (0)

Range B(std::size_t o, std::size_t l) { return {Style::Bold, o, l, ""}; }
Range I(std::size_t o, std::size_t l) { return {Style::Italic, o, l, ""}; }
Range C(std::size_t o, std::size_t l) { return {Style::Code, o, l, ""}; }
Range L(std::size_t o, std::size_t l, std::string url) { return {Style::Link, o, l, std::move(url)}; }

void expect(const std::string& input, const std::string& text, const std::vector<Range>& ranges) {
    ++g_checks;
    const auto msg = msgfmt::parse(input);
    if (msg.text != text || msg.ranges != ranges) {
        ++g_failures;
        std::cerr << "parse(" << input << "):\n  got      \"" << msg.text << "\" "
                  << show(msg.ranges) << "\n  expected \"" << text << "\" " << show(ranges)
                  << "\n";
    }
}

void expect_literal(const std::string& input) { expect(input, input, {}); }

void test_basic_styles() {
    expect("", "", {});
    expect("Привет, **мир**!", "Привет, мир!", {B(8, 3)});
    expect("_курсив_", "курсив", {I(0, 6)});
    expect("`code`", "code", {C(0, 4)});
    expect("[сайт](https://vk.com)", "сайт", {L(0, 4, "https://vk.com")});
    expect("**a**b", "ab", {B(0, 1)});
    expect("[a](b)c", "ac", {L(0, 1, "b")});
    expect("(_a_)", "(a)", {I(1, 1)});
    expect("a\n**b**", "a\nb", {B(2, 1)});
}

void test_word_boundaries() {
    expect_literal("a_b_c");
    expect_literal("snake_case_name");
    expect_literal("при_вет_");
    expect_literal("_a_b");
    expect("x _y_.", "x y.", {I(2, 1)});
    expect_literal("_ a_");
}

void test_whitespace_rules() {
    expect_literal("2 ** 3");
    expect_literal("** 3");
    expect_literal("**a **");
    expect_literal("` x`");
    expect_literal("[ a](u)");
    expect_literal("[a ](u)");
}

void test_incomplete_constructions() {
    expect_literal("**не закрыто");
    expect_literal("_не закрыто");
    expect_literal("`не закрыто");
    expect_literal("[текст]()");
    expect_literal("[текст]");
    expect_literal("[a](b");
    expect_literal("[a b](u v)");
    expect_literal("[](u)");
    expect_literal("**");
    expect_literal("****");
    // Экранированный маркер не может закрыть стиль.
    expect("**a\\**", "**a**", {});
}

void test_code_is_raw() {
    expect("`code **x**`", "code **x**", {C(0, 10)});
    expect("`a\\*b`", "a\\*b", {C(0, 4)});
    expect("`[a](b)`", "[a](b)", {C(0, 6)});
}

void test_escaping() {
    expect("2 \\* 3", "2 * 3", {});
    expect("\\\\", "\\", {});
    expect("\\q", "\\q", {});
    expect("\\_a_", "_a_", {});
    expect("\\[a](b)", "[a](b)", {});
    expect("**a\\\\**", "a\\", {B(0, 2)});
    expect("\\", "\\", {});
}

void test_nesting() {
    expect("**жирный _и_ курсив**", "жирный и курсив", {B(0, 15), I(7, 1)});
    expect("**_x_**", "x", {B(0, 1), I(0, 1)});
    expect("_**x**_", "x", {I(0, 1), B(0, 1)});
    expect("[**жирная** ссылка](u)", "жирная ссылка", {L(0, 13, "u"), B(0, 6)});
    expect("**[x](u)**", "x", {B(0, 1), L(0, 1, "u")});
    // Маркер того же стиля внутри открытого стиля - обычный текст.
    expect("**a **b** c**", "a **b c**", {B(0, 5)});
    expect("_a _b_ c_", "a _b c_", {I(0, 4)});
}

void test_utf16_offsets() {
    expect("**😀**", "😀", {B(0, 2)});
    expect("😀 _a_", "😀 a", {I(3, 1)});
    expect("𝔸𝔹 [x](u)", "𝔸𝔹 x", {L(5, 1, "u")});
    // Нормализация не выполняется: «й» одной и двумя кодовыми точками.
    expect("\xD0\xB9 **x**", "\xD0\xB9 x", {B(2, 1)});
    expect("\xD0\xB8\xCC\x86 **x**", "\xD0\xB8\xCC\x86 x", {B(3, 1)});
}

void test_invalid_utf8() {
    const std::vector<std::string> bad = {
        "\xFF", "\x80", "\xC0\x80", "\xC3", "\xE2\x82", "\xED\xA0\x80",
        "\xF4\x90\x80\x80", "\xF8\x88\x80\x80\x80", "ok\xC3(", "\xE0\x80\xAF",
    };
    for (const auto& s : bad) {
        CHECK(!msgfmt::is_valid_utf8(s));
        bool thrown = false;
        try {
            msgfmt::parse(s);
        } catch (const msgfmt::InvalidUtf8&) {
            thrown = true;
        }
        CHECK(thrown);
        thrown = false;
        try {
            msgfmt::escape(s);
        } catch (const msgfmt::InvalidUtf8&) {
            thrown = true;
        }
        CHECK(thrown);
    }
    CHECK(msgfmt::is_valid_utf8("\xF4\x8F\xBF\xBF"));  // U+10FFFF
    CHECK(msgfmt::is_valid_utf8(std::string("a\0b", 3)));
}

void test_escape_function() {
    CHECK(msgfmt::escape("2 * 3") == "2 \\* 3");
    CHECK(msgfmt::escape("*_`[]()\\") == "\\*\\_\\`\\[\\]\\(\\)\\\\");
    CHECK(msgfmt::escape("привет") == "привет");
    CHECK(msgfmt::escape("") == "");
}

void test_json() {
    CHECK(msgfmt::to_json(msgfmt::parse("Привет, **мир**!")) ==
          "{\"text\": \"Привет, мир!\", \"ranges\": [{\"style\": \"bold\", \"offset\": 8, "
          "\"length\": 3, \"url\": \"\"}]}");
    CHECK(msgfmt::to_json(msgfmt::parse(std::string("\"a\\\\\n\t\0", 7))) ==
          "{\"text\": \"\\\"a\\\\\\n\\t\\u0000\", \"ranges\": []}");
    CHECK(msgfmt::to_json(msgfmt::parse("[x](a\"b)")) ==
          "{\"text\": \"x\", \"ranges\": [{\"style\": \"link\", \"offset\": 0, \"length\": 1, "
          "\"url\": \"a\\\"b\"}]}");
}

std::size_t utf16_length(const std::string& s) {
    std::size_t n = 0;
    for (const char ch : s) {
        const auto c = static_cast<unsigned char>(ch);
        if ((c & 0xC0) != 0x80) n += (c >= 0xF0) ? 2 : 1;
    }
    return n;
}

// Свойства на случайных входах (фиксированное зерно - детерминированно):
// диапазоны в пределах текста, отсортированы, без частичных пересечений;
// parse(escape(x)) возвращает x без диапазонов.
void test_properties() {
    const std::vector<std::string> alphabet = {"*", "*", "_", "`", "[", "]", "(", ")", "\\",
                                               " ", "a", "b", "ж", "😀", "\n", "u"};
    std::mt19937 rng(12345);
    for (int iter = 0; iter < 20000; ++iter) {
        std::string input;
        const int len = static_cast<int>(rng() % 24);
        for (int k = 0; k < len; ++k) input += alphabet[rng() % alphabet.size()];

        const auto msg = msgfmt::parse(input);
        const std::size_t total = utf16_length(msg.text);
        bool ok = true;
        for (std::size_t a = 0; a < msg.ranges.size(); ++a) {
            const auto& r = msg.ranges[a];
            if (r.length == 0 || r.offset + r.length > total) ok = false;
            if ((r.style == Style::Link) == r.url.empty()) ok = false;
            if (a > 0) {
                const auto& p = msg.ranges[a - 1];
                if (p.offset > r.offset || (p.offset == r.offset && p.length < r.length)) ok = false;
            }
            for (std::size_t b = 0; b < a; ++b) {
                const auto& q = msg.ranges[b];
                const bool disjoint = q.offset + q.length <= r.offset || r.offset + r.length <= q.offset;
                const bool nested = (q.offset <= r.offset && r.offset + r.length <= q.offset + q.length) ||
                                    (r.offset <= q.offset && q.offset + q.length <= r.offset + r.length);
                if (!disjoint && !nested) ok = false;
            }
        }
        const auto round = msgfmt::parse(msgfmt::escape(input));
        if (round.text != input || !round.ranges.empty()) ok = false;
        ++g_checks;
        if (!ok) {
            ++g_failures;
            std::cerr << "property violated for input: " << input << " -> " << msg.text << " "
                      << show(msg.ranges) << "\n";
        }
    }
}

}  // namespace

int main() {
    test_basic_styles();
    test_word_boundaries();
    test_whitespace_rules();
    test_incomplete_constructions();
    test_code_is_raw();
    test_escaping();
    test_nesting();
    test_utf16_offsets();
    test_invalid_utf8();
    test_escape_function();
    test_json();
    test_properties();
    std::cout << g_checks << " checks, " << g_failures << " failures\n";
    return g_failures == 0 ? 0 : 1;
}
