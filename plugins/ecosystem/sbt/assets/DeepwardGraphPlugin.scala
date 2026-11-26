// Injected by deepward into the sbt meta-build (never written to the
// repository): it adds the deepwardGraph task to every project, which appends
// the resolved update report of each configuration as one JSON line.
package deepward

import sbt._
import sbt.Keys._

object DeepwardGraphPlugin extends AutoPlugin {
  override def trigger = allRequirements
  override def requires = sbt.plugins.JvmPlugin

  object autoImport {
    val deepwardGraph = taskKey[Unit]("deepward: write the resolved dependency graph as JSON lines")
  }
  import autoImport._

  private val wanted = Set("compile", "runtime", "test", "provided", "optional")
  private val backslash = 92.toChar
  private val quote = 34.toChar

  private def q(s: String): String = {
    val sb = new StringBuilder
    sb.append(quote)
    s.foreach { c =>
      if (c == quote || c == backslash) { sb.append(backslash); sb.append(c) }
      else if (c < ' ') sb.append(backslash).append("u").append("%04x".format(c.toInt))
      else sb.append(c)
    }
    sb.append(quote).toString
  }

  private def mid(m: ModuleID): String = q(m.organization + ":" + m.name + ":" + m.revision)

  override def projectSettings: Seq[Setting[_]] = Seq(
    deepwardGraph := {
      val out = sys.props.get("deepward.out").map(file).getOrElse(target.value / "deepward-graph.jsonl")
      val report = updateFull.value
      val configs = report.configurations.filter(c => wanted(c.configuration.name)).map { c =>
        val modules = c.modules.map { m =>
          val callers = m.callers.map(cl => mid(cl.caller)).mkString("[", ",", "]")
          "{" + q("id") + ":" + mid(m.module) + "," + q("evicted") + ":" + m.evicted + "," + q("callers") + ":" + callers + "}"
        }.mkString("[", ",", "]")
        "{" + q("name") + ":" + q(c.configuration.name) + "," + q("modules") + ":" + modules + "}"
      }.mkString("[", ",", "]")
      val line = "{" + q("project") + ":" + q(thisProject.value.id) + "," + q("module") + ":" + mid(projectID.value) + "," +
        q("scalaVersion") + ":" + q(scalaVersion.value) + "," + q("scalaBinaryVersion") + ":" + q(scalaBinaryVersion.value) + "," +
        q("configurations") + ":" + configs + "}"
      IO.append(out, line + "\n")
    }
  )
}
