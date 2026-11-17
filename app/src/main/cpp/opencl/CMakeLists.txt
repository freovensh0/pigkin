# pigkin-opencl-loader, the OpenCL functions that load the vendor's libOpenCL.so on first use, for ggml's OpenCL backend
# in pigkin-llama, and pigkin-opencl-probe, which tells the app which GPU OpenCL reports.

# Khronos's OpenCL headers, fetched by Gradle's llamaSources task.
if(NOT OPENCL_HEADERS)
  message(FATAL_ERROR "OPENCL_HEADERS is not set")
endif()

add_library(pigkin-opencl-loader STATIC opencl_loader.c)
target_include_directories(pigkin-opencl-loader PUBLIC ${OPENCL_HEADERS})
target_compile_options(pigkin-opencl-loader PRIVATE -Wall -Wextra -Werror)
target_link_libraries(pigkin-opencl-loader PRIVATE log)

add_library(pigkin-opencl-probe SHARED opencl_probe.c)
target_compile_options(pigkin-opencl-probe PRIVATE -Wall -Wextra -Werror)
target_link_libraries(pigkin-opencl-probe PRIVATE pigkin-opencl-loader)
