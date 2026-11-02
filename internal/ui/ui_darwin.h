#include <stddef.h>
#include <stdint.h>

// Implemented in window.m. All functions may be called from any thread unless noted.
void ui_init(const char *title); // main thread, before ui_run
void ui_run(void);               // main thread; runs the Cocoa event loop, never returns
void ui_set_logs_dir(const char *dir);
void ui_set_status(const char *text);
void ui_set_on_top(int on);
void ui_show_settings(const char *name, int hevc, int fps, int showStatus, int onTop);

// Video: parameter sets are concatenated in ps, sizes[i] is the length of the i-th one.
// Returns an OSStatus (0 on success).
int ui_video_config(int hevc, const uint8_t *ps, const size_t *sizes, int count, int width, int height);
void ui_video_frame(const uint8_t *avcc, size_t len); // 4-byte length-prefixed NAL units
void ui_video_clear(void);
// Frames shown since the last call, the size of the last one and the time from
// receiving a frame to putting it on screen (decode). Resets the counters.
int ui_video_stats(int *width, int *height, double *avgMs, double *maxMs);

// Implemented in Go (exports.go).
extern void goOpenSettings(void);
extern void goApplySettings(char *name, int hevc, int fps, int showStatus, int onTop);
extern void goWillTerminate(void);
extern void goLog(char *msg);
