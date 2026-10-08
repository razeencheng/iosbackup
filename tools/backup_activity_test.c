/* Exercise the patched CLI against fake mobilebackup2 I/O, without a device.
 * Compile in the pinned libimobiledevice build tree with its normal libraries. */
#define main upstream_backup_main
#define mobilebackup2_send_raw test_send_raw
#define mobilebackup2_receive_raw test_receive_raw
#define mobilebackup2_receive_message test_receive_message
#include "idevicebackup2.c"
#undef main
#undef mobilebackup2_send_raw
#undef mobilebackup2_receive_raw
#undef mobilebackup2_receive_message
#include <assert.h>

static mobilebackup2_error_t message_result = MOBILEBACKUP2_E_RECEIVE_TIMEOUT;
mobilebackup2_error_t test_send_raw(mobilebackup2_client_t c, const char *p, uint32_t n, uint32_t *sent)
{
    (void)c; (void)p; *sent=n; return MOBILEBACKUP2_E_SUCCESS;
}
mobilebackup2_error_t test_receive_raw(mobilebackup2_client_t c, char *p, uint32_t n, uint32_t *received)
{
    (void)c; (void)p; *received=n; return MOBILEBACKUP2_E_SUCCESS;
}
mobilebackup2_error_t test_receive_message(mobilebackup2_client_t c, plist_t *p, char **id)
{
    (void)c; (void)p; (void)id; return message_result;
}

int main(void)
{
    FILE *capture=tmpfile();
    assert(capture);
    int saved=dup(STDERR_FILENO);
    assert(saved>=0 && dup2(fileno(capture),STDERR_FILENO)>=0);
    iosbk_activity_enabled=1;
    iosbk_activity("waiting_authorization",0,1);
    plist_t message=NULL;
    char *id=NULL;
    assert(iosbk_receive_message(NULL,&message,&id)==MOBILEBACKUP2_E_RECEIVE_TIMEOUT);
    fflush(stderr);
    rewind(capture);
    char output[4096]={0};
    size_t length=fread(output,1,sizeof(output)-1,capture);
    assert(length>0);
    /* Waiting for a response is still authorization until a real response. */
    assert(strstr(output,"IOSBK_ACTIVITY 0 waiting_authorization\n"));
    if (strstr(output,"waiting_device")) {
        puts("FAIL: receive timeout incorrectly left authorization phase");
        fflush(stdout);
        return 1;
    }
    fseek(capture,0,SEEK_END);
    message_result=MOBILEBACKUP2_E_SUCCESS;
    assert(iosbk_receive_message(NULL,&message,&id)==MOBILEBACKUP2_E_SUCCESS);
    uint32_t count=999;
    char buf[8]={0};
    assert(iosbk_send_raw(NULL,buf,3,&count)==MOBILEBACKUP2_E_SUCCESS && count==3);
    assert(iosbk_receive_raw(NULL,buf,5,&count)==MOBILEBACKUP2_E_SUCCESS && count==5);
    iosbk_activity("waiting_device",0,1);
    fflush(stderr);
    rewind(capture);
    memset(output,0,sizeof(output));
    length=fread(output,1,sizeof(output)-1,capture);
    assert(length>0);
    assert(strstr(output,"IOSBK_ACTIVITY 1 waiting_device\n"));
    assert(strstr(output,"IOSBK_ACTIVITY 9 waiting_device\n"));
    assert(dup2(saved,STDERR_FILENO)>=0);
    close(saved);fclose(capture);
    puts("backup activity: authorization timeout, successful message, byte counters passed");
    return 0;
}
