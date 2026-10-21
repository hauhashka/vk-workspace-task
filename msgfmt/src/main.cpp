// Командная строка msgfmt:
//   msgfmt          - разбор stdin, JSON в stdout;
//   msgfmt escape   - экранирование служебных символов stdin.
// Коды возврата: 0 - успех, 2 - некорректный UTF-8, 1 - неверные аргументы.
#include <cstdio>
#include <iostream>
#include <string>

#include "msgfmt/msgfmt.hpp"

namespace {

std::string read_stdin() {
    std::string data;
    char buf[65536];
    std::size_t n;
    while ((n = std::fread(buf, 1, sizeof buf, stdin)) > 0) data.append(buf, n);
    return data;
}

void write_stdout(const std::string& s) {
    std::fwrite(s.data(), 1, s.size(), stdout);
    std::fflush(stdout);
}

}  // namespace

int main(int argc, char** argv) {
    bool escape_mode = false;
    if (argc == 2 && std::string(argv[1]) == "escape") {
        escape_mode = true;
    } else if (argc != 1) {
        std::cerr << "usage: msgfmt [escape] < input\n";
        return 1;
    }

    const std::string input = read_stdin();
    try {
        if (escape_mode) {
            write_stdout(msgfmt::escape(input));
        } else {
            write_stdout(msgfmt::to_json(msgfmt::parse(input)) + "\n");
        }
    } catch (const msgfmt::InvalidUtf8&) {
        write_stdout("{\"error\": \"invalid_utf8\"}\n");
        return 2;
    }
    return 0;
}
