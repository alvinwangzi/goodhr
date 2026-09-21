/** 本文件负责将根路径重定向到登录页。 */
import { redirect } from "next/navigation";

/** RootPage 访问首页时直接跳到登录页。 */
export default function RootPage() {
  redirect("/login");
}
