cmake_minimum_required(VERSION 3.20)
project(msgfmt LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

option(MSGFMT_SANITIZE "Build with -fsanitize=address,undefined" OFF)
option(MSGFMT_WERROR "Treat warnings as errors" OFF)

add_compile_options(-Wall -Wextra)
if(MSGFMT_WERROR)
  add_compile_options(-Werror)
endif()
if(MSGFMT_SANITIZE)
  add_compile_options(-fsanitize=address,undefined -fno-omit-frame-pointer -fno-sanitize-recover=all)
  add_link_options(-fsanitize=address,undefined)
endif()

add_library(msgfmt_lib STATIC src/msgfmt.cpp src/utf8.cpp)
target_include_directories(msgfmt_lib PUBLIC include PRIVATE src)

add_executable(msgfmt src/main.cpp)
target_link_libraries(msgfmt PRIVATE msgfmt_lib)

enable_testing()

add_executable(test_msgfmt tests/test_msgfmt.cpp)
target_link_libraries(test_msgfmt PRIVATE msgfmt_lib)
add_test(NAME unit COMMAND test_msgfmt)

# Тесты командной строки: вход, ожидаемый stdout и код возврата.
add_test(NAME cli COMMAND ${CMAKE_COMMAND}
  -DMSGFMT=$<TARGET_FILE:msgfmt>
  -DCASES=${CMAKE_CURRENT_SOURCE_DIR}/tests/cli_cases
  -P ${CMAKE_CURRENT_SOURCE_DIR}/tests/run_cli.cmake)
