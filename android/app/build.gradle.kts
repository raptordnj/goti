plugins { id("com.android.application") }

val signingEnv = listOf("ANDROID_KEYSTORE_FILE", "ANDROID_KEYSTORE_PASSWORD", "ANDROID_KEY_ALIAS", "ANDROID_KEY_PASSWORD")
    .associateWith { System.getenv(it).orEmpty() }
val signingReady = signingEnv.values.all { it.isNotEmpty() }

android {
    namespace = "io.github.raptordnj.goti"
    compileSdk = 35

    defaultConfig {
        applicationId = "io.github.raptordnj.goti"
        minSdk = 26
        targetSdk = 35
        versionName = (project.findProperty("versionName") as String?) ?: "0.0.0-dev"
        versionCode = (project.findProperty("versionCode") as String?)?.toInt() ?: 1
    }

    signingConfigs {
        create("release") {
            if (signingReady) {
                storeFile = file(signingEnv.getValue("ANDROID_KEYSTORE_FILE"))
                storePassword = signingEnv.getValue("ANDROID_KEYSTORE_PASSWORD")
                keyAlias = signingEnv.getValue("ANDROID_KEY_ALIAS")
                keyPassword = signingEnv.getValue("ANDROID_KEY_PASSWORD")
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            signingConfig = signingConfigs.getByName("release")
        }
    }
}

// Release builds must be signed with the real key: never fall back to debug.
tasks.configureEach {
    if (name == "preReleaseBuild") {
        doFirst {
            if (!signingReady) {
                throw GradleException("Release signing secrets missing: set " + signingEnv.keys.joinToString(", "))
            }
        }
    }
}

dependencies {
    implementation(files("libs/goti.aar"))
    testImplementation("junit:junit:4.13.2")
}
