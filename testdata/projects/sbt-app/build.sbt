ThisBuild / organization := "com.acme"
ThisBuild / version := "0.1.0"
ThisBuild / scalaVersion := "2.13.14"

// Shared version used by several circe modules.
val circeVersion = "0.14.5"

lazy val core = (project in file("core"))
  .settings(
    libraryDependencies ++= Seq(
      "org.typelevel" %% "cats-core" % "2.9.0",
      "io.circe" %% "circe-core" % circeVersion,
      "io.circe" %% "circe-parser" % circeVersion,
      "org.apache.logging.log4j" % "log4j-core" % "2.14.1",
      "org.scalatest" %% "scalatest" % "3.2.15" % Test
    )
  )

lazy val root = (project in file("."))
  .aggregate(core)
  .dependsOn(core)
  .settings(
    name := "sbt-app",
    libraryDependencies += "com.fasterxml.jackson.core" % "jackson-databind" % "2.12.1"
  )
