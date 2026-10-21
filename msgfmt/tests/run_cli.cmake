# Запускает msgfmt на каждом случае из каталога CASES:
#   <имя>.in   - stdin,
#   <имя>.out  - ожидаемый stdout (побайтно),
#   <имя>.code - ожидаемый код возврата (по умолчанию 0).
# Случаи с префиксом escape_ запускаются как `msgfmt escape`.
file(GLOB inputs "${CASES}/*.in")
set(failed 0)
foreach(input IN LISTS inputs)
  get_filename_component(name "${input}" NAME_WE)
  set(args "")
  if(name MATCHES "^escape_")
    set(args escape)
  endif()
  set(expected_code 0)
  if(EXISTS "${CASES}/${name}.code")
    file(STRINGS "${CASES}/${name}.code" expected_code LIMIT_COUNT 1)
  endif()
  file(READ "${CASES}/${name}.out" expected_hex HEX)
  execute_process(COMMAND "${MSGFMT}" ${args}
    INPUT_FILE "${input}"
    OUTPUT_FILE "${CMAKE_CURRENT_BINARY_DIR}/${name}.actual"
    RESULT_VARIABLE code)
  file(READ "${CMAKE_CURRENT_BINARY_DIR}/${name}.actual" actual_hex HEX)
  if(NOT code STREQUAL expected_code OR NOT actual_hex STREQUAL expected_hex)
    message(SEND_ERROR "case ${name}: code ${code} (expected ${expected_code}), output differs: ${actual_hex} vs ${expected_hex}")
    set(failed 1)
  endif()
endforeach()
if(failed)
  message(FATAL_ERROR "cli tests failed")
endif()
