#include "utf8.hpp"

namespace msgfmt::detail {

bool decode_utf8(std::string_view input, std::vector<char32_t>& out) {
    out.clear();
    out.reserve(input.size());
    const auto* p = reinterpret_cast<const unsigned char*>(input.data());
    const std::size_t n = input.size();
    std::size_t i = 0;
    while (i < n) {
        const unsigned char b0 = p[i];
        if (b0 < 0x80) {
            out.push_back(b0);
            ++i;
            continue;
        }
        std::size_t len;
        char32_t cp;
        char32_t min;
        if ((b0 & 0xE0) == 0xC0) {
            len = 2; cp = b0 & 0x1F; min = 0x80;
        } else if ((b0 & 0xF0) == 0xE0) {
            len = 3; cp = b0 & 0x0F; min = 0x800;
        } else if ((b0 & 0xF8) == 0xF0) {
            len = 4; cp = b0 & 0x07; min = 0x10000;
        } else {
            return false;  // продолжающий байт или 0xF8..0xFF
        }
        if (n - i < len) return false;
        for (std::size_t k = 1; k < len; ++k) {
            const unsigned char b = p[i + k];
            if ((b & 0xC0) != 0x80) return false;
            cp = (cp << 6) | (b & 0x3F);
        }
        if (cp < min) return false;                      // overlong
        if (cp > 0x10FFFF) return false;                 // вне Unicode
        if (cp >= 0xD800 && cp <= 0xDFFF) return false;  // суррогаты
        out.push_back(cp);
        i += len;
    }
    return true;
}

void append_utf8(std::string& out, char32_t cp) {
    if (cp < 0x80) {
        out.push_back(static_cast<char>(cp));
    } else if (cp < 0x800) {
        out.push_back(static_cast<char>(0xC0 | (cp >> 6)));
        out.push_back(static_cast<char>(0x80 | (cp & 0x3F)));
    } else if (cp < 0x10000) {
        out.push_back(static_cast<char>(0xE0 | (cp >> 12)));
        out.push_back(static_cast<char>(0x80 | ((cp >> 6) & 0x3F)));
        out.push_back(static_cast<char>(0x80 | (cp & 0x3F)));
    } else {
        out.push_back(static_cast<char>(0xF0 | (cp >> 18)));
        out.push_back(static_cast<char>(0x80 | ((cp >> 12) & 0x3F)));
        out.push_back(static_cast<char>(0x80 | ((cp >> 6) & 0x3F)));
        out.push_back(static_cast<char>(0x80 | (cp & 0x3F)));
    }
}

bool is_space(char32_t cp) {
    switch (cp) {
        case 0x09: case 0x0A: case 0x0B: case 0x0C: case 0x0D: case 0x20:
        case 0x85: case 0xA0: case 0x1680:
        case 0x2028: case 0x2029: case 0x202F: case 0x205F: case 0x3000:
            return true;
        default:
            return cp >= 0x2000 && cp <= 0x200A;
    }
}

namespace {

struct Interval {
    char32_t lo, hi;
};

// Диапазоны букв и цифр основных письменностей. Это осознанное упрощение
// вместо полной таблицы Unicode (см. README, «Известные ограничения»).
constexpr Interval kAlnum[] = {
    {U'0', U'9'},     {U'A', U'Z'},     {U'a', U'z'},
    {0x00AA, 0x00AA}, {0x00B5, 0x00B5}, {0x00BA, 0x00BA},
    {0x00C0, 0x00D6}, {0x00D8, 0x00F6}, {0x00F8, 0x02AF},  // латиница
    {0x0370, 0x0373}, {0x0376, 0x0377}, {0x037B, 0x037D},
    {0x0386, 0x0386}, {0x0388, 0x03FF},                    // греческий
    {0x0400, 0x0481}, {0x048A, 0x052F},                    // кириллица
    {0x0531, 0x0556}, {0x0561, 0x0587},                    // армянский
    {0x05D0, 0x05EA}, {0x0620, 0x064A}, {0x0660, 0x0669},  // иврит, арабский
    {0x0E01, 0x0E30},                                      // тайский
    {0x10A0, 0x10FF},                                      // грузинский
    {0x1E00, 0x1FFF},                                      // расширенная латиница, греческий
    {0x3041, 0x3096}, {0x30A1, 0x30FA},                    // кана
    {0x3400, 0x4DBF}, {0x4E00, 0x9FFF},                    // CJK
    {0xAC00, 0xD7A3},                                      // хангыль
    {0xFF10, 0xFF19}, {0xFF21, 0xFF3A}, {0xFF41, 0xFF5A},  // полноширинные
};

}  // namespace

bool is_alnum(char32_t cp) {
    for (const auto& r : kAlnum) {
        if (cp < r.lo) return false;
        if (cp <= r.hi) return true;
    }
    return false;
}

}  // namespace msgfmt::detail
