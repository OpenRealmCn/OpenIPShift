import './style.css';
export const metadata = {
  title: 'OpenIPShift · IPv4 轮换控制台',
  description: 'OpenRealm 系列：可自定义节点数量、访问 Token 保护的 IPv4 轮换模拟控制台。当前仅 Mock，不修改 AWS 或 DNS。',
  applicationName: 'OpenIPShift',
  icons: { icon: '/openipshift.svg' },
};
export default function Layout({children}:{children:React.ReactNode}) {return <html lang="zh-CN"><body>{children}</body></html>}
