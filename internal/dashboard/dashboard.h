void dashboardInit(const char *html);
void dashboardRun(void);
void dashboardStop(void);
void dashboardStartupError(const char *message);
void dashboardMenuBar(int enabled, int active, int mode);
void dashboardShow(void);
void dashboardQuit(void);
void dashboardSend(const char *json);
void dashboardPickFiles(int model);
void dashboardCopy(const char *text);
void dashboardCopyURL(const char *text);
void dashboardSave(const char *name, const char *text);
void dashboardTop(int enabled);
void dashboardPlay(const char *path, double seconds);

void dashboardActivate(void);
