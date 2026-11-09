#include <stdint.h>

typedef struct TalaSystemInput TalaSystemInput;
TalaSystemInput *tala_system_open(int32_t *status);
int tala_system_rate(TalaSystemInput *input);
int tala_system_read(TalaSystemInput *input, float *samples, int capacity);
uint64_t tala_system_first_time(TalaSystemInput *input);
uint64_t tala_system_time(void);
void tala_system_stop(TalaSystemInput *input);
void tala_system_close(TalaSystemInput *input);
