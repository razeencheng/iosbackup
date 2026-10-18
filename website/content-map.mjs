// 只收录公开用户文档；新页面必须显式登记，不能递归导入 docs/。
export const groups = [
  {en:'Get started', zh:'开始使用', ids:['getting-started','overview','synology','installation','quickstart']},
  {en:'Back up devices', zh:'备份设备', ids:['usb-backup','wifi','scheduling','encryption','backup-browser','notifications','states']},
  {en:'Restore data', zh:'恢复数据', ids:['restore','restore-finder','restore-apple-devices','experimental','inspection']},
  {en:'Maintain your deployment', zh:'维护部署', ids:['device-storage','upgrade','instance-recovery','troubleshooting','wifi-nat']},
  {en:'Reference', zh:'参考', ids:['configuration','feature-status','operations']},
];
const labels = {
  'getting-started':['Get started','开始使用'], overview:['Requirements','环境与支持范围'],
  synology:['Synology NAS','群晖安装'], installation:['Linux / Docker Compose','Linux 安装'],
  quickstart:['Quick start with screenshots','图解快速开始'], 'usb-backup':['Your first USB backup','首次 USB 备份'],
  wifi:['Wi-Fi backups','Wi-Fi 备份'], scheduling:['Automatic schedules','自动计划'],
  encryption:['Encryption and passwords','加密与密码'], 'backup-browser':['Browse local backups','查看本地备份'], notifications:['Notifications','通知'],
  states:['States and progress','状态与进度'], restore:['Restore overview','恢复总览'],
  'restore-finder':['Finder on Mac','Mac Finder 恢复'], 'restore-apple-devices':['Apple Devices on Windows','Windows Apple Devices 恢复'],
  experimental:['Experimental operations','站内实验操作'], inspection:['Inspect backups','查看备份与文件清单'],
  'device-storage':['Devices and storage','设备与存储'], upgrade:['Upgrade and roll back','升级与回滚'],
  'instance-recovery':['Recover the deployment','迁移与恢复部署实例'], troubleshooting:['Troubleshooting','故障排查'],
  'wifi-nat':['Advanced Wi-Fi / NAT','高级 Wi-Fi / NAT'], configuration:['Configuration','配置参考'],
  'feature-status':['Feature status','功能状态'], operations:['Operations index','操作手册索引'],
};
const special = {quickstart:'docs/QUICKSTART', 'feature-status':'docs/FEATURE_STATUS', operations:'docs/OPERATIONS'};
export const pages = Object.entries(labels).flatMap(([id, label]) => ['en','zh-cn'].map(locale => ({
  id, locale, label:label[locale === 'en' ? 0 : 1],
  source:`${special[id] || `docs/manual/${id}`}${locale === 'en' ? '' : '.zh-CN'}.md`,
  route:`/${locale}/docs/${id}/`,
})));
export const publicReferences = new Set([
  'README.md','README.zh-CN.md','CONTRIBUTING.md','SECURITY.md','SUPPORT.md','CODE_OF_CONDUCT.md',
  'LICENSE','NOTICE','TRADEMARKS.md','THIRD_PARTY_NOTICES.md','CHANGELOG.md','compose.yaml',
  'docs/PRIVACY.md','docs/LICENSING.md','docs/DEVELOPMENT.md','docs/DEVELOPMENT.zh-CN.md',
  'docs/TESTING_GUIDE.md','docs/TESTING_GUIDE.en.md','docs/WEBHOOK_GUIDE.md','docs/WEBHOOK_GUIDE.en.md',
  'docs/NOTIFICATION_SYSTEM.md','docs/NOTIFICATION_SYSTEM.en.md',
  'docs/NOTIFICATION_SECURITY.md','docs/NOTIFICATION_SECURITY.en.md','docs/images/tutorial/README.md',
]);
export const repository = 'https://github.com/razeencheng/iosbackup';
